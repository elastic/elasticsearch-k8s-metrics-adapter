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

package lister

import (
	"context"
	"sync"
	"time"

	"github.com/elastic/elasticsearch-adapter/pkg/common"
	"github.com/elastic/elasticsearch-adapter/pkg/config"
	"github.com/elastic/elasticsearch-adapter/pkg/provider/providers/elasticsearch"
	"github.com/elastic/elasticsearch-adapter/pkg/provider/providers/upstream"
	"github.com/elastic/elasticsearch-adapter/pkg/tracing"
	esv7 "github.com/elastic/go-elasticsearch/v7"
	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
	"go.elastic.co/apm"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
	custom_metrics_client "k8s.io/metrics/pkg/client/custom_metrics"
)

type MetricLister struct {
	cfg      *config.Config
	esClient *esv7.Client

	// K8S Clients
	client dynamic.Interface
	mapper apimeta.RESTMapper

	// Upstream clients
	upstreamMetricClient custom_metrics_client.CustomMetricsClient
	upstreamRestClient   *discovery.DiscoveryClient

	defaultMetricsProvider provider.MetricsProvider

	// Current state
	currentCustomMetrics []provider.CustomMetricInfo
	metadata             map[string]common.MetricMetadata

	// sync variables
	once sync.Once
	m    sync.RWMutex

	tracer *apm.Tracer
}

func NewMetricLister(
	cfg *config.Config,
	client dynamic.Interface,
	mapper apimeta.RESTMapper,
	esClient *esv7.Client,
	tracer *apm.Tracer,
) *MetricLister {
	return &MetricLister{
		cfg:                  cfg,
		esClient:             esClient,
		client:               client,
		mapper:               mapper,
		once:                 sync.Once{},
		currentCustomMetrics: nil,
		m:                    sync.RWMutex{},
		tracer:               tracer,
	}
}

func NewMetricListerWithUpstream(
	cfg *config.Config,
	esClient *esv7.Client,
	client dynamic.Interface,
	mapper apimeta.RESTMapper,
	upstreamMetricClient custom_metrics_client.CustomMetricsClient,
	upstreamRestClient *discovery.DiscoveryClient,
	tracer *apm.Tracer,
) *MetricLister {
	return &MetricLister{
		cfg:                  cfg,
		esClient:             esClient,
		client:               client,
		mapper:               mapper,
		upstreamMetricClient: upstreamMetricClient,
		upstreamRestClient:   upstreamRestClient,
		once:                 sync.Once{},
		currentCustomMetrics: nil,
		m:                    sync.RWMutex{},
		tracer:               tracer,
	}
}

func (ml *MetricLister) GetMetricsProvider(metric string) provider.MetricsProvider {
	ml.m.RLock()
	defer ml.m.RUnlock()
	metadata, exists := ml.metadata[metric]
	if !exists {
		return ml.defaultMetricsProvider
	}
	return metadata.MetricsProvider
}

func (ml *MetricLister) GetMetricMetadata(ctx *context.Context, metric string) *common.MetricMetadata {
	defer tracing.Span(ctx)()
	ml.m.RLock()
	defer ml.m.RUnlock()
	metadata, exists := ml.metadata[metric]
	if !exists {
		return nil
	}
	return &metadata
}

func (ml *MetricLister) ListAllMetrics() []provider.CustomMetricInfo {
	t, _ := tracing.NewTransaction(context.TODO(), ml.tracer, "metric_lister", "ListAllMetrics")
	defer tracing.EndTransaction(t)
	klog.Info("-> metric_lister.ListAllMetrics()")
	ml.m.RLock()
	defer ml.m.RUnlock()
	ccm := ml.currentCustomMetrics
	klog.Infof("-> metric_lister.ListAllMetrics() - size = %d", len(ccm))
	return ccm
}

func (ml *MetricLister) Start() {
	ml.once.Do(
		func() {
			t, _ := tracing.NewTransaction(context.TODO(), ml.tracer, "metric_lister", "Start")
			defer tracing.EndTransaction(t)

			esProvider := elasticsearch.NewProvider(ml.client, ml.mapper, ml.esClient, ml, ml.tracer)
			ml.m.Lock()
			defer ml.m.Unlock()
			attempts := 10
			sleep := 5 * time.Second
			for i := 0; ; i++ {
				klog.Infof("Fetching metric list from Elasticsearch, attempt %d", i)
				metrics, metadata, err := ml.elasticsearchMetrics(esProvider)
				if err == nil {
					klog.Infof("%d metrics listed from Elasticsearch", len(metrics))
					ml.currentCustomMetrics = metrics
					ml.metadata = metadata
					break
				}
				if i >= (attempts - 1) {
					klog.Fatalf("Give up after %d attempts", attempts)
				}
				klog.Errorf("Error while fetching metrics list from Elasticsearch: %v, will retry in %s", err, sleep)
				time.Sleep(sleep)
			}

			if !ml.cfg.Upstream.ClientConfig.IsDefined() {
				return
			}
			upstreamProvider := upstream.NewUpstreamProvider(ml.mapper, ml.upstreamMetricClient, ml, ml.tracer)
			ml.defaultMetricsProvider = upstreamProvider
			attempts = 10
			for i := 0; ; i++ {
				klog.Infof("Fetching metric list from upstream metric adapter %v, attempt %d", ml.cfg.Upstream.ClientConfig.Host, i)
				metrics, metadata, err := upstreamMetrics(ml.upstreamRestClient, upstreamProvider)
				if err == nil {
					klog.Infof("%d metrics listed from upstream metric adapter %v", len(metrics), ml.cfg.Upstream.ClientConfig.Host)
					ml.currentCustomMetrics = append(ml.currentCustomMetrics, metrics...)
					for k, v := range metadata {
						ml.metadata[k] = v
					}
					return
				}
				if i >= (attempts - 1) {
					break
				}
				klog.Errorf("Error while fetching metrics list from upstream %v: %v, will retry in %s", ml.cfg.Upstream, err, sleep)
				time.Sleep(sleep)
			}
			klog.Fatalf("Give up after %d attempts", attempts)
		},
	)
}
