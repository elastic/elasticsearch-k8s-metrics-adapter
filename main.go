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

	"github.com/elastic/elasticsearch-adapter/pkg/common"
	"github.com/elastic/elasticsearch-adapter/pkg/config"
	"github.com/elastic/elasticsearch-adapter/pkg/lister"
	provider2 "github.com/elastic/elasticsearch-adapter/pkg/provider"
	esclient "github.com/elastic/elasticsearch-adapter/pkg/provider/providers/elasticsearch/client"
	"github.com/elastic/elasticsearch-adapter/pkg/tracing"
	"go.elastic.co/apm"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	custom_metrics_client "k8s.io/metrics/pkg/client/custom_metrics"

	// Load all auth plugins
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/component-base/logs"
	"k8s.io/klog/v2"

	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/apiserver"
	basecmd "github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/cmd"
	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
	openapinamer "k8s.io/apiserver/pkg/endpoints/openapi"
	genericapiserver "k8s.io/apiserver/pkg/server"

	generatedopenapi "github.com/elastic/elasticsearch-adapter/generated/openapi"
)

type ElasticsearchAdapter struct {
	basecmd.AdapterBase
	Insecure bool
}

func (a *ElasticsearchAdapter) makeProviderOrDie() provider.MetricsProvider {
	adapterCfg, err := config.Default()
	if err != nil {
		klog.Fatalf("unable to parse adapter configuration: %v", err)
	}

	client, err := a.DynamicClient()
	if err != nil {
		klog.Fatalf("unable to construct dynamic client: %v", err)
	}

	mapper, err := a.RESTMapper()
	if err != nil {
		klog.Fatalf("unable to construct client REST mapper: %v", err)
	}

	esClient, err := esclient.NewElasticsearchClient(adapterCfg.Elasticsearch.ClientConfig)
	if err != nil {
		klog.Fatalf("unable to construct Elasticsearch client: %v", err)
	}

	tracer := createTracer()

	var metricLister common.MetricLister
	if adapterCfg.Upstream.ClientConfig.IsDefined() {
		discoveryClient, err := a.DiscoveryClient()
		if err != nil {
			klog.Fatalf("unable to construct discoveryClient client: %v", err)
		}
		apiVersionsGetter := custom_metrics_client.NewAvailableAPIsGetter(discoveryClient)

		clientCfg, err := a.ClientConfig()
		if err != nil {
			klog.Fatalf("unable to construct Kubernetes client config: %v", err)
		}
		kubeClient, err := kubernetes.NewForConfig(clientCfg)
		if err != nil {
			klog.Fatalf("unable to construct Kubernetes client: %v", err)
		}
		upstreamConfig, err := adapterCfg.Upstream.ClientConfig.NewRestClientConfig(kubeClient, clientCfg)
		if err != nil {
			klog.Fatalf("unable to construct discovery client for upstream: %v", err)
		}
		upstreamRestClient, err := discovery.NewDiscoveryClientForConfig(upstreamConfig)
		if err != nil {
			klog.Fatalf("unable to construct discovery client for upstream: %v", err)
		}
		upstreamMetricClient := custom_metrics_client.NewForConfig(upstreamConfig, mapper, apiVersionsGetter)
		metricLister = lister.NewMetricListerWithUpstream(adapterCfg, esClient, client, mapper, upstreamMetricClient, upstreamRestClient, tracer)
	} else {
		metricLister = lister.NewMetricLister(adapterCfg, client, mapper, esClient, tracer)
	}

	// Create and start metric lister
	metricLister.Start()

	// Create provider
	return provider2.NewAggregationProvider(metricLister, tracer)
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
	cmd.Flags().AddGoFlagSet(flag.CommandLine) // make sure we get the klog flags
	cmd.Flags().Parse(os.Args)

	elasticsearchProvider := cmd.makeProviderOrDie()
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
