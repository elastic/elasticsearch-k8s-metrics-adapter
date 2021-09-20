package client

import (
	"github.com/elastic/elasticsearch-adapter/pkg/config"
	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/metrics/pkg/apis/custom_metrics"
	"k8s.io/metrics/pkg/apis/external_metrics"
)

type Interface interface {
	GetConfiguration() config.MetricServer

	ListCustomMetricInfos() (map[provider.CustomMetricInfo]struct{}, error)
	GetMetricByName(name types.NamespacedName, info provider.CustomMetricInfo, selector labels.Selector) (*custom_metrics.MetricValue, error)
	GetMetricBySelector(namespace string, selector labels.Selector, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValueList, error)

	ListExternalMetrics() (map[provider.ExternalMetricInfo]struct{}, error)
	GetExternalMetric(name, namespace string, selector labels.Selector) (*external_metrics.ExternalMetricValueList, error)
}
