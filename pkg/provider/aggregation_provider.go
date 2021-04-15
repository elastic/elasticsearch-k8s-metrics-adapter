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

package provider

import (
	"errors"

	"github.com/elastic/elasticsearch-adapter/pkg/common"
	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"k8s.io/metrics/pkg/apis/custom_metrics"
	"k8s.io/metrics/pkg/apis/external_metrics"
)

// elasticsearchProvider is an implementation of provider.MetricsProvider which retrieve metrics from an Elasticsearch cluster.
type aggregationProvider struct {
	common.MetricLister
}

// NewAggregationProvider returns an instance of the Elasticsearch provider, along with its restful.WebService that opens endpoints to post custom metrics stored in Elasticsearch.
func NewAggregationProvider(
	metricLister common.MetricLister,
) provider.MetricsProvider {
	return &aggregationProvider{
		MetricLister: metricLister,
	}
}

func (p *aggregationProvider) GetMetricByName(name types.NamespacedName, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValue, error) {
	klog.Infof("-> aggregation.GetMetricByName(name=%v,info=%v,metricSelector=%v)", name, info, metricSelector)
	metricMetadata := p.MetricLister.GetMetricMetadata(info.Metric)
	if metricMetadata == nil {
		return nil, provider.NewMetricNotFoundError(info.GroupResource, info.Metric)
	}
	return metricMetadata.MetricsProvider.GetMetricByName(name, info, metricSelector)
}

func (p *aggregationProvider) GetMetricBySelector(namespace string, selector labels.Selector, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValueList, error) {
	klog.Infof("-> aggregation.GetMetricBySelector(namespace=%v,selector=%v,info=%v,metricSelector=%v)", namespace, selector, info, metricSelector)
	metricMetadata := p.MetricLister.GetMetricMetadata(info.Metric)
	if metricMetadata == nil {
		return nil, provider.NewMetricNotFoundForSelectorError(info.GroupResource, info.Metric, "", metricSelector)
	}
	return metricMetadata.MetricsProvider.GetMetricBySelector(namespace, selector, info, metricSelector)
}

func (p *aggregationProvider) GetExternalMetric(_ string, _ labels.Selector, _ provider.ExternalMetricInfo) (*external_metrics.ExternalMetricValueList, error) {
	// TODO: Implement
	return nil, errors.New("GetExternalMetric not implemented")
}

func (p *aggregationProvider) ListAllExternalMetrics() []provider.ExternalMetricInfo {
	klog.Error("not implemented: ListAllExternalMetrics()")
	return []provider.ExternalMetricInfo{}
}
