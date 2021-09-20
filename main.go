// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. Elasticsearch B.V. licenses this file to
// you under the Apache License, Version 2.0 (the "License");
// you may  not use this file except in compliance with the
// License.
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
	"os"

	"github.com/elastic/elasticsearch-adapter/pkg/client/custom_api"
	"github.com/elastic/elasticsearch-adapter/pkg/client/elasticsearch"
	"github.com/elastic/elasticsearch-adapter/pkg/config"
	"github.com/elastic/elasticsearch-adapter/pkg/monitoring"
	"github.com/elastic/elasticsearch-adapter/pkg/provider"
	"github.com/elastic/elasticsearch-adapter/pkg/registry"
	"github.com/elastic/elasticsearch-adapter/pkg/scheduler"
	"github.com/elastic/elasticsearch-adapter/pkg/tracing"
	"go.elastic.co/apm"
	"k8s.io/client-go/kubernetes"

	// Load all auth plugins
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/component-base/logs"
	"k8s.io/klog/v2"

	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/apiserver"
	basecmd "github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/cmd"
	cm_provider "github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
	openapinamer "k8s.io/apiserver/pkg/endpoints/openapi"
	genericapiserver "k8s.io/apiserver/pkg/server"

	generatedopenapi "github.com/elastic/elasticsearch-adapter/generated/openapi"
)

type ElasticsearchAdapter struct {
	basecmd.AdapterBase
	monitoringServer *monitoring.Server

	Insecure                 bool
	PrometheusMetricsEnabled bool
	MonitoringPort           int
}

func (a *ElasticsearchAdapter) makeProviderOrDie(adapterCfg *config.Config) cm_provider.MetricsProvider {
	client, err := a.DynamicClient()
	if err != nil {
		klog.Fatalf("unable to construct dynamic client: %v", err)
	}

	mapper, err := a.RESTMapper()
	if err != nil {
		klog.Fatalf("unable to construct client REST mapper: %v", err)
	}

	tracer := createTracer()

	esMetricClient, err := elasticsearch.NewElasticsearchClient(
		adapterCfg.Elasticsearch,
		client,
		mapper,
		tracer,
	)
	if err != nil {
		klog.Fatalf("unable to construct Elasticsearch client: %v", err)
	}
	scheduler := scheduler.NewScheduler(esMetricClient)

	if adapterCfg.Upstream != nil {
		clientCfg, err := a.ClientConfig()
		if err != nil {
			klog.Fatalf("unable to construct Kubernetes client config: %v", err)
		}
		kubeClient, err := kubernetes.NewForConfig(clientCfg)
		if err != nil {
			klog.Fatalf("unable to construct Kubernetes client: %v", err)
		}
		metricApiClient, err := custom_api.NewMetricApiClientProvider(clientCfg, mapper).NewClient(kubeClient, *adapterCfg.Upstream)
		if err != nil {
			klog.Fatalf("unable to construct Kubernetes custom metric API client: %v", err)
		}
		scheduler.WithClients(metricApiClient)
	}

	r := registry.NewRegistry()
	scheduler.
		WithMetricListeners(a.monitoringServer, r).
		WithErrorListeners(a.monitoringServer).
		Start().
		WaitInitialSync()
	return provider.NewAggregationProvider(r, tracer)
}

func createTracer() *apm.Tracer {
	if tracing.IsEnabled() {
		t, err := apm.NewTracer("elasticsearch-metrics-adapter", "0.0.1")
		if err != nil {
			// don't fail the application because tracing fails
			klog.Errorf("failed to created tracer: %s ", err)
			return nil
		}
		t.SetLogger(&tracing.Logger{})
		return t
	}
	return nil
}

func main() {
	logs.InitLogs()
	defer logs.FlushLogs()

	cmd := &ElasticsearchAdapter{}

	cmd.OpenAPIConfig = genericapiserver.DefaultOpenAPIConfig(generatedopenapi.GetOpenAPIDefinitions, openapinamer.NewDefinitionNamer(apiserver.Scheme))
	cmd.OpenAPIConfig.Info.Title = "elasticsearch-adapter"
	cmd.OpenAPIConfig.Info.Version = "0.1.0"

	cmd.Flags().BoolVar(&cmd.Insecure, "insecure", false, "if true authentication and authorization are disabled, only to be used in dev mode")
	cmd.Flags().IntVar(&cmd.MonitoringPort, "monitoring-port", 9090, "port to expose readiness and Prometheus metrics")
	cmd.Flags().BoolVar(&cmd.PrometheusMetricsEnabled, "enable-metrics", false, "enable Prometheus metrics endpoint /metrics on the monitoring port")
	cmd.Flags().AddGoFlagSet(flag.CommandLine) // make sure we get the klog flags
	err := cmd.Flags().Parse(os.Args)
	if err != nil {
		klog.Fatalf("unable to parse flags: %v", err)
	}

	// Parse the adapter configuration
	adapterCfg, err := config.Default()
	if err != nil {
		klog.Fatalf("unable to parse adapter configuration: %v", err)
	}

	cmd.monitoringServer = monitoring.NewServer(adapterCfg, cmd.MonitoringPort, cmd.PrometheusMetricsEnabled)
	go cmd.monitoringServer.Start()

	elasticsearchProvider := cmd.makeProviderOrDie(adapterCfg)
	cmd.WithCustomMetrics(elasticsearchProvider)
	cmd.WithExternalMetrics(elasticsearchProvider)
	if cmd.Insecure {
		cmd.Authentication = nil
		cmd.Authorization = nil
	}

	klog.Info("starting Elasticsearch adapter...")
	if err := cmd.Run(wait.NeverStop); err != nil {
		klog.Fatalf("unable to run Elasticsearch custom metrics adapter: %v", err)
	}
}
