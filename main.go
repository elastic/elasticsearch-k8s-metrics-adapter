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
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/go-logr/logr"

	cmprovider "sigs.k8s.io/custom-metrics-apiserver/pkg/provider"

	// Load all auth plugins
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	openapinamer "k8s.io/apiserver/pkg/endpoints/openapi"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/kubernetes"
	"k8s.io/component-base/logs"
	"sigs.k8s.io/custom-metrics-apiserver/pkg/apiserver"
	basecmd "sigs.k8s.io/custom-metrics-apiserver/pkg/cmd"

	"go.elastic.co/apm/v2"

	_ "github.com/KimMachineGun/automemlimit"

	generatedopenapi "github.com/elastic/elasticsearch-k8s-metrics-adapter/generated/openapi"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/client"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/client/custom_api"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/client/elasticsearch"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/config"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/hpa"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/log"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/monitoring"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/profiling"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/provider"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/registry"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/scheduler"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/tracing"
)

const (
	serviceType                  = "elasticsearch-k8s-metrics-adapter"
	elastisearchMetricServerType = "elasticsearch"
	customMetricServerType       = "custom"

	discoveryModePeriodic = "periodic"
	discoveryModeHPA      = "hpa"
)

var (
	serviceVersion string
	logger         logr.Logger
)

func main() {
	cmd := &ElasticsearchAdapter{}
	cmd.OpenAPIConfig = genericapiserver.DefaultOpenAPIConfig(generatedopenapi.GetOpenAPIDefinitions, openapinamer.NewDefinitionNamer(apiserver.Scheme))
	cmd.OpenAPIConfig.Info.Title = serviceType
	cmd.OpenAPIConfig.Info.Version = serviceVersion

	logs.AddFlags(cmd.Flags())
	cmd.Flags().BoolVar(&cmd.Insecure, "insecure", false, "if true authentication and authorization are disabled, only to be used in dev mode")
	cmd.Flags().IntVar(&cmd.MonitoringPort, "monitoring-port", 9090, "port to expose readiness and Prometheus metrics")
	cmd.Flags().IntVar(&cmd.ProfilingPort, "profiling-port", 0, "port to expose pprof profiling")
	cmd.Flags().StringVar(&cmd.DiscoveryMode, "discovery-mode", discoveryModePeriodic,
		"how Elasticsearch metric discovery is performed: "+
			"'periodic' (default) fetches the full index mapping every minute; "+
			"'hpa' watches HorizontalPodAutoscaler objects and resolves only the metrics they reference via the _field_caps API")
	cmd.Flags().DurationVar(&cmd.NegativeCacheTTL, "negative-cache-ttl", registry.DefaultNegativeCacheTTL,
		"how long the resolver caches a 'metric not found' result before retrying (hpa discovery mode only)")
	cmd.Flags().AddGoFlagSet(flag.CommandLine) // make sure we get the klog flags
	err := cmd.Flags().Parse(os.Args)
	if err != nil {
		logErrorAndExit(err, "Unable to parse flags")
	}

	flushLogs := log.Configure(cmd.Flags(), serviceType, serviceVersion)
	defer flushLogs()
	logger = log.ForPackage("main")

	switch cmd.DiscoveryMode {
	case discoveryModePeriodic, discoveryModeHPA:
	default:
		logErrorAndExit(
			fmt.Errorf("invalid value %q (expected %q or %q)", cmd.DiscoveryMode, discoveryModePeriodic, discoveryModeHPA),
			"Invalid --discovery-mode")
	}

	adapterCfg, err := config.Parse()
	if err != nil {
		logErrorAndExit(err, "Unable to parse adapter configuration")
	}

	logger.Info("Starting monitoring server...")
	monitoringServer := monitoring.NewServer(adapterCfg.MetricServers, cmd.MonitoringPort, adapterCfg.ReadinessProbe.FailureThreshold)
	go monitoringServer.Start()

	if cmd.ProfilingPort > 0 {
		logger.Info("Starting profiling server...")
		go profiling.StartProfiling(cmd.ProfilingPort)
	}

	apmTracer, err := apm.NewTracer(serviceType, serviceVersion)
	if err != nil {
		logErrorAndExit(err, "Unable to create APM tracer")
	}
	apmTracer.SetLogger(&tracing.Logger{})

	metricsClients, err := cmd.newMetricsClients(adapterCfg, apmTracer)
	if err != nil {
		logErrorAndExit(err, "Unable to create metrics provider")
	}

	// In hpa mode, Elasticsearch clients skip the periodic scheduler (and its
	// full _mapping scan) entirely; metrics are resolved on demand via
	// _field_caps, driven by the HPA watcher. Other client types (custom_api)
	// still go through periodic discovery because their list endpoints are cheap.
	var scheduledClients []client.Interface
	var resolverClients []client.Interface
	if cmd.DiscoveryMode == discoveryModeHPA {
		for _, c := range metricsClients {
			if c.GetConfiguration().ServerType == elastisearchMetricServerType {
				resolverClients = append(resolverClients, c)
			} else {
				scheduledClients = append(scheduledClients, c)
			}
		}
		logger.Info("Discovery mode is hpa",
			"resolver_clients", len(resolverClients),
			"scheduled_clients", len(scheduledClients),
			"negative_cache_ttl", cmd.NegativeCacheTTL,
		)
	} else {
		scheduledClients = metricsClients
		logger.Info("Discovery mode is periodic")
	}

	metricsRegistry := registry.NewRegistry()
	if len(resolverClients) > 0 {
		metricsRegistry.WithResolver(registry.NewResolver(resolverClients, cmd.NegativeCacheTTL))
		// Resolver-backed clients never receive a periodic "first sync" event, so
		// seed the monitoring server's readiness counters with a synthetic empty
		// update so /readyz doesn't block indefinitely on them.
		for _, c := range resolverClients {
			monitoringServer.UpdateCustomMetrics(c, map[cmprovider.CustomMetricInfo]struct{}{})
			// Seed external metrics too: MetricTypes defaults to "serve all types",
			// so the monitoring server tracks external metrics for the ES client even
			// though the ES client doesn't support them. Without this, the external
			// success counter stays 0 and /readyz returns 503 indefinitely.
			monitoringServer.UpdateExternalMetrics(c, map[cmprovider.ExternalMetricInfo]struct{}{})
		}
	}

	// In hpa mode, watch HorizontalPodAutoscaler objects and proactively
	// advertise the metrics they reference. This is required because the
	// Kubernetes API server only routes a custom metric request to the adapter
	// if the metric is already advertised — a purely lazy resolve-on-request
	// approach returns 404 before reaching us.
	if cmd.DiscoveryMode == discoveryModeHPA {
		cmd.startHPAWatcher(metricsRegistry)
	}

	sched := scheduler.NewScheduler(scheduledClients...)
	sched.
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
	if err := cmd.Run(context.WithoutCancel(context.Background())); err != nil {
		logErrorAndExit(err, "Unable to run elastic k8s metrics adapter")
	}
}

type ElasticsearchAdapter struct {
	basecmd.AdapterBase

	Insecure                 bool
	PrometheusMetricsEnabled bool
	MonitoringPort           int
	ProfilingPort            int
	DiscoveryMode            string
	NegativeCacheTTL         time.Duration
}

// hpaWatcherResyncPeriod drives the informer's full relist, which also retries
// any metric resolutions that failed transiently.
const hpaWatcherResyncPeriod = 10 * time.Minute

// startHPAWatcher builds a Kubernetes clientset and starts the HPA watcher,
// blocking until its cache has synced so the registry is warm before the API
// server starts routing metric requests.
func (a *ElasticsearchAdapter) startHPAWatcher(metricsRegistry *registry.Registry) {
	clientCfg, err := a.ClientConfig()
	if err != nil {
		logErrorAndExit(err, "Unable to construct Kubernetes client config for HPA watcher")
	}
	clientset, err := kubernetes.NewForConfig(clientCfg)
	if err != nil {
		logErrorAndExit(err, "Unable to construct Kubernetes clientset for HPA watcher")
	}
	watcher := hpa.NewWatcher(clientset, metricsRegistry, hpaWatcherResyncPeriod)
	if err := watcher.Start(context.Background()); err != nil {
		logErrorAndExit(err, "HPA watcher failed to start")
	}
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
