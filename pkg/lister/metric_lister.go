package lister

import (
	"sync"
	"time"

	"github.com/elastic/elasticsearch-adapter/pkg/common"
	"github.com/elastic/elasticsearch-adapter/pkg/config"
	"github.com/elastic/elasticsearch-adapter/pkg/provider/providers/elasticsearch"
	"github.com/elastic/elasticsearch-adapter/pkg/provider/providers/upstream"
	esv7 "github.com/elastic/go-elasticsearch/v7"
	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
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
}

func NewMetricLister(
	cfg *config.Config,
	client dynamic.Interface,
	mapper apimeta.RESTMapper,
	esClient *esv7.Client,
) *MetricLister {
	return &MetricLister{
		cfg:                  cfg,
		esClient:             esClient,
		client:               client,
		mapper:               mapper,
		once:                 sync.Once{},
		currentCustomMetrics: nil,
		m:                    sync.RWMutex{},
	}
}

func NewMetricListerWithUpstream(
	cfg *config.Config,
	esClient *esv7.Client,
	client dynamic.Interface,
	mapper apimeta.RESTMapper,
	upstreamMetricClient custom_metrics_client.CustomMetricsClient,
	upstreamRestClient *discovery.DiscoveryClient,
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

func (ml *MetricLister) GetMetricMetadata(metric string) *common.MetricMetadata {
	ml.m.RLock()
	defer ml.m.RUnlock()
	metadata, exists := ml.metadata[metric]
	if !exists {
		return nil
	}
	return &metadata
}

func (ml *MetricLister) ListAllMetrics() []provider.CustomMetricInfo {
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
			esProvider := elasticsearch.NewProvider(ml.client, ml.mapper, ml.esClient, ml)
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

			if !ml.cfg.Upstream.IsDefined() {
				return
			}
			upstreamProvider := upstream.NewUpstreamProvider(ml.mapper, ml.upstreamMetricClient, ml)
			ml.defaultMetricsProvider = upstreamProvider
			attempts = 10
			for i := 0; ; i++ {
				klog.Infof("Fetching metric list from upstream metric adapter %v, attempt %d", ml.cfg.Upstream.Host, i)
				metrics, metadata, err := upstreamMetrics(ml.upstreamRestClient, upstreamProvider)
				if err == nil {
					klog.Infof("%d metrics listed from upstream metric adapter %v", len(metrics), ml.cfg.Upstream.Host)
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
