package common

import (
	"context"

	"github.com/elastic/elasticsearch-adapter/pkg/config"
	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
)

type MetricLister interface {
	Start()

	// ListAllMetrics lists all the metrics available from Elasticsearch and the upstream metrics server.
	ListAllMetrics() []provider.CustomMetricInfo

	// GetMetricMetadata returns some metadata for a given metric.
	GetMetricMetadata(ctx *context.Context, metric string) *MetricMetadata

	// GetMetricsProvider returns the metrics provider to be used to get a metric or nil if none.
	GetMetricsProvider(metric string) provider.MetricsProvider
}

type MetricMetadata struct {
	Fields          config.Fields
	Search          *config.Search
	Indices         []string
	MetricsProvider provider.MetricsProvider
}
