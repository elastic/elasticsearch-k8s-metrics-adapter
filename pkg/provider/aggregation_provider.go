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
	"fmt"

	"github.com/elastic/elasticsearch-adapter/pkg/registry"
	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
	"go.elastic.co/apm"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"k8s.io/metrics/pkg/apis/custom_metrics"
	"k8s.io/metrics/pkg/apis/external_metrics"
)

// aggregationProvider is an implementation of provider.MetricsProvider which retrieve metrics from a set of metric clients.
type aggregationProvider struct {
	registry *registry.Registry
	tracer   *apm.Tracer
}

// NewAggregationProvider returns an instance of the aggregation provider.
func NewAggregationProvider(
	registry *registry.Registry,
	tracer *apm.Tracer,
) provider.MetricsProvider {
	return &aggregationProvider{
		registry: registry,
		tracer:   tracer,
	}
}

func (p *aggregationProvider) GetMetricByName(name types.NamespacedName, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValue, error) {
	klog.Infof("-> aggregation.GetMetricByName(name=%v,info=%v,metricSelector=%v)", name, info, metricSelector)
	metricClient, err := p.registry.GetCustomMetricClient(info)
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics backend: %v", err)
	}
	return metricClient.GetMetricByName(name, info, metricSelector)
}

func (p *aggregationProvider) GetMetricBySelector(namespace string, selector labels.Selector, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValueList, error) {
	klog.Infof("-> aggregation.GetMetricBySelector(namespace=%v,selector=%v,info=%v,metricSelector=%v)", namespace, selector, info, metricSelector)
	metricClient, err := p.registry.GetCustomMetricClient(info)
	if err != nil {
		return nil, err
	}
	return metricClient.GetMetricBySelector(namespace, selector, info, metricSelector)
}

func (p *aggregationProvider) GetExternalMetric(namespace string, metricSelector labels.Selector, info provider.ExternalMetricInfo) (*external_metrics.ExternalMetricValueList, error) {
	metricClient, err := p.registry.GetExternalMetricClient(info)
	if err != nil {
		return nil, err
	}
	return metricClient.GetExternalMetric(info.Metric, namespace, metricSelector)
}

func (p *aggregationProvider) ListAllMetrics() []provider.CustomMetricInfo {
	return p.registry.ListAllCustomMetrics()
}

func (p *aggregationProvider) ListAllExternalMetrics() []provider.ExternalMetricInfo {
	return p.registry.ListAllExternalMetrics()
}
