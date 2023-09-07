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

package provider

import (
	"context"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/metrics/pkg/apis/custom_metrics"
	"k8s.io/metrics/pkg/apis/external_metrics"
	"sigs.k8s.io/custom-metrics-apiserver/pkg/provider"

	"go.elastic.co/apm"

	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/log"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/registry"
)

// aggregationProvider is an implementation of provider.MetricsProvider which retrieve metrics from a set of metric clients.
type aggregationProvider struct {
	logger   logr.Logger
	registry *registry.Registry
	tracer   *apm.Tracer
}

// NewAggregationProvider returns an instance of the aggregation provider.
func NewAggregationProvider(
	registry *registry.Registry,
	tracer *apm.Tracer,
) provider.MetricsProvider {
	return &aggregationProvider{
		logger:   log.ForPackage("provider"),
		registry: registry,
		tracer:   tracer,
	}
}

func (p *aggregationProvider) GetMetricByName(context context.Context, name types.NamespacedName, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValue, error) {
	p.logger.Info("GetMetricByName", "name", name, "info", info, "metricSelector", metricSelector)
	metricClient, err := p.registry.GetCustomMetricClient(info)
	if err != nil {
		return nil, err
	}
	return metricClient.GetMetricByName(name, info, metricSelector)
}

func (p *aggregationProvider) GetMetricBySelector(context context.Context, namespace string, selector labels.Selector, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValueList, error) {
	p.logger.Info("GetMetricBySelector", "namespace", namespace, "selector", selector, "info", info, "metricSelector", metricSelector)
	metricClient, err := p.registry.GetCustomMetricClient(info)
	if err != nil {
		return nil, err
	}
	return metricClient.GetMetricBySelector(namespace, selector, info, metricSelector)
}

func (p *aggregationProvider) GetExternalMetric(context context.Context, namespace string, metricSelector labels.Selector, info provider.ExternalMetricInfo) (*external_metrics.ExternalMetricValueList, error) {
	p.logger.Info("GetExternalMetric", "namespace", namespace, "info", info, "metricSelector", metricSelector)
	metricClient, err := p.registry.GetExternalMetricClient(info)
	if err != nil {
		return nil, err
	}
	return metricClient.GetExternalMetric(info.Metric, namespace, metricSelector)
}

func (p *aggregationProvider) ListAllMetrics() []provider.CustomMetricInfo {
	p.logger.Info("ListAllMetrics")
	return p.registry.ListAllCustomMetrics()
}

func (p *aggregationProvider) ListAllExternalMetrics() []provider.ExternalMetricInfo {
	p.logger.Info("ListAllExternalMetrics")
	return p.registry.ListAllExternalMetrics()
}
