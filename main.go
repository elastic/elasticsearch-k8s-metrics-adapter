// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE.txt file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/go-logr/logr"

	// Load all auth plugins
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/util/wait"
	openapinamer "k8s.io/apiserver/pkg/endpoints/openapi"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/kubernetes"
	"k8s.io/component-base/logs"
	"sigs.k8s.io/custom-metrics-apiserver/pkg/apiserver"
	basecmd "sigs.k8s.io/custom-metrics-apiserver/pkg/cmd"

	"go.elastic.co/apm"

	generatedopenapi "github.com/elastic/elasticsearch-k8s-metrics-adapter/generated/openapi"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/client"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/client/custom_api"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/client/elasticsearch"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/config"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/log"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/monitoring"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/provider"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/registry"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/scheduler"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/tracing"
)

const (
	serviceType    = "elasticsearch-k8s-metrics-adapter"
	serviceVersion = "0.0.0"

	elastisearchMetricServerType = "elasticsearch"
	customMetricServerType       = "custom"
)

var (
	logger logr.Logger
)

func main() {
	cmd := &ElasticsearchAdapter{}
	cmd.OpenAPIConfig = genericapiserver.DefaultOpenAPIConfig(generatedopenapi.GetOpenAPIDefinitions, openapinamer.NewDefinitionNamer(apiserver.Scheme))
	cmd.OpenAPIConfig.Info.Title = serviceType
	cmd.OpenAPIConfig.Info.Version = serviceVersion

	logs.AddFlags(cmd.Flags())
	cmd.Flags().BoolVar(&cmd.Insecure, "insecure", false, "if true authentication and authorization are disabled, only to be used in dev mode")
	cmd.Flags().IntVar(&cmd.MonitoringPort, "monitoring-port", 9090, "port to expose readiness and Prometheus metrics")
	cmd.Flags().AddGoFlagSet(flag.CommandLine) // make sure we get the klog flags
	err := cmd.Flags().Parse(os.Args)
	if err != nil {
		logErrorAndExit(err, "Unable to parse flags")
	}

	flushLogs := log.Configure(cmd.Flags(), serviceType, serviceVersion)
	defer flushLogs()
	logger = log.ForPackage("main")

	adapterCfg, err := config.Parse()
	if err != nil {
		logErrorAndExit(err, "Unable to parse adapter configuration")
	}

	logger.Info("Starting monitoring server...")
	monitoringServer := monitoring.NewServer(adapterCfg.MetricServers, cmd.MonitoringPort, adapterCfg.ReadinessProbe.FailureThreshold)
	go monitoringServer.Start()

	apmTracer, err := apm.NewTracer(serviceType, serviceVersion)
	if err != nil {
		logErrorAndExit(err, "Unable to create APM tracer")
	}
	apmTracer.SetLogger(&tracing.Logger{})

	metricsClients, err := cmd.newMetricsClients(adapterCfg, apmTracer)
	if err != nil {
		logErrorAndExit(err, "Unable to create metrics provider")
	}

	scheduler := scheduler.NewScheduler(metricsClients...)
	metricsRegistry := registry.NewRegistry()
	scheduler.
		WithMetricListeners(monitoringServer, metricsRegistry).
		WithErrorListeners(monitoringServer).
		Start().
		WaitInitialSync()
	aggProvider := provider.NewAggregationProvider(metricsRegistry, apmTracer)

	cmd.WithCustomMetrics(aggProvider)
	cmd.WithExternalMetrics(aggProvider)
	if cmd.Insecure {
		cmd.Authentication = nil
		cmd.Authorization = nil
	}

	logger.Info("Starting elastic k8s metrics adapter...")
	if err := cmd.Run(wait.NeverStop); err != nil {
		logErrorAndExit(err, "Unable to run elastic k8s metrics adapter")
	}
}

type ElasticsearchAdapter struct {
	basecmd.AdapterBase

	Insecure                 bool
	PrometheusMetricsEnabled bool
	MonitoringPort           int
}

func (a *ElasticsearchAdapter) newMetricsClients(adapterCfg *config.Config, tracer *apm.Tracer) ([]client.Interface, error) {
	dynamicClient, err := a.DynamicClient()
	if err != nil {
		return nil, fmt.Errorf("unable to construct dynamic dynamicClient: %w", err)
	}

	mapper, err := a.RESTMapper()
	if err != nil {
		return nil, fmt.Errorf("unable to construct dynamicClient REST mapper: %w", err)
	}

	var clients []client.Interface
	for _, clientCfg := range adapterCfg.MetricServers {
		switch clientCfg.ServerType {
		case elastisearchMetricServerType:
			esMetricClient, err := elasticsearch.NewElasticsearchClient(
				clientCfg,
				dynamicClient,
				mapper,
				tracer,
			)
			if err != nil {
				return nil, fmt.Errorf("unable to construct Elasticsearch dynamicClient: %w", err)
			}
			clients = append(clients, esMetricClient)
		case customMetricServerType:
			kubeClientCfg, err := a.ClientConfig()
			if err != nil {
				return nil, fmt.Errorf("unable to construct Kubernetes dynamicClient config: %w", err)
			}
			kubeClient, err := kubernetes.NewForConfig(kubeClientCfg)
			if err != nil {
				return nil, fmt.Errorf("unable to construct Kubernetes dynamicClient: %w", err)
			}
			metricApiClient, err := custom_api.NewMetricApiClientProvider(kubeClientCfg, mapper).NewClient(kubeClient, clientCfg)
			if err != nil {
				return nil, fmt.Errorf("unable to construct Kubernetes custom metric API dynamicClient: %w", err)
			}
			clients = append(clients, metricApiClient)
		}

	}

	return clients, nil
}

func logErrorAndExit(err error, msg string) {
	logger.Error(err, msg)
	os.Exit(1)
}
