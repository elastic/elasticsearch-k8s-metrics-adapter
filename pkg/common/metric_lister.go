package common

import (
	"github.com/elastic/elasticsearch-adapter/pkg/config"
	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
)

type MetricLister interface {
	Start()

	ListAllMetrics() []provider.CustomMetricInfo
	GetMetricMetadata(metric string) *MetricMetadata
}

type MetricMetadata struct {
	Fields          config.Fields
	Search          *config.Search
	Indices         []string
	MetricsProvider provider.MetricsProvider
}
