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

package elasticsearch

import (
	"errors"
	"fmt"

	"github.com/elastic/elasticsearch-adapter/pkg/config"
	esv7 "github.com/elastic/go-elasticsearch/v7"
	apierr "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
	"k8s.io/metrics/pkg/apis/custom_metrics"
	"k8s.io/metrics/pkg/apis/external_metrics"

	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider/helpers"
)

// CustomMetricResource wraps provider.CustomMetricInfo in a struct which stores the Name and Namespace of the resource
// So that we can accurately store and retrieve the metric as if this were an actual metrics server.
type CustomMetricResource struct {
	provider.CustomMetricInfo
	types.NamespacedName
}

// elasticsearchProvider is an implementation of provider.MetricsProvider which retrieve metrics from an Elasticsearch cluster.
type elasticsearchProvider struct {
	config       *config.Config
	client       dynamic.Interface
	mapper       apimeta.RESTMapper
	esClient     *esv7.Client
	metricLister *config.MetricLister
}

// NewProvider returns an instance of the Elasticsearch provider, along with its restful.WebService that opens endpoints to post custom metrics stored in Elasticsearch.
func NewProvider(
	cfg *config.Config,
	client dynamic.Interface,
	mapper apimeta.RESTMapper,
	esClient *esv7.Client,
) provider.MetricsProvider {
	metricLister := config.NewMetricLister(cfg, esClient)
	klog.Info("Discovering initial metrics set")
	metricLister.Start()
	klog.Info("Successfully discovered initial metrics set")
	return &elasticsearchProvider{
		metricLister: metricLister,
		config:       cfg,
		client:       client,
		mapper:       mapper,
		esClient:     esClient,
	}
}

// valueFor is a helper function to get just the value of a specific metric
func (p *elasticsearchProvider) valueFor(
	info provider.CustomMetricInfo,
	name types.NamespacedName,
	originalSelector labels.Selector,
	objects []string,
	metricSelector labels.Selector,
) (timestampedMetric, error) {
	info, _, err := info.Normalized(p.mapper)
	if err != nil {
		return timestampedMetric{}, err
	}

	metadata := p.metricLister.GetMetricMetadata(info.Metric)
	if metadata == nil {
		return timestampedMetric{}, fmt.Errorf("no metadata for metric %s", info.Metric)
	}
	value, err := getMetricForPod(p.esClient, *metadata, name, info, metricSelector, originalSelector, objects)
	if err != nil {
		return timestampedMetric{}, err
	}

	/*if !metricSelector.Matches(value.labels) {
		return resource.Quantity{}, provider.NewMetricNotFoundForSelectorError(info.GroupResource, info.Metric, name.Name, metricSelector)
	}*/

	return value, nil

}

// metricFor is a helper function which formats a value, metric, and object info into a MetricValue which can be returned by the metrics API
func (p *elasticsearchProvider) metricFor(
	timeStampedMetric timestampedMetric,
	name types.NamespacedName,
	selector labels.Selector,
	info provider.CustomMetricInfo,
	metricSelector labels.Selector,
) (*custom_metrics.MetricValue, error) {
	objRef, err := helpers.ReferenceFor(p.mapper, name, info)
	if err != nil {
		return nil, err
	}

	metric := &custom_metrics.MetricValue{
		DescribedObject: objRef,
		Metric: custom_metrics.MetricIdentifier{
			Name: info.Metric,
		},
		Timestamp: timeStampedMetric.timestamp,
		Value:     timeStampedMetric.value,
	}

	if len(metricSelector.String()) > 0 {
		sel, err := metav1.ParseToLabelSelector(metricSelector.String())
		if err != nil {
			return nil, err
		}
		metric.Metric.Selector = sel
	}

	return metric, nil
}

// metricsFor is a wrapper used by GetMetricBySelector to format several metrics which match a resource selector
func (p *elasticsearchProvider) metricsFor(namespace string, selector labels.Selector, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValueList, error) {
	klog.Infof(fmt.Sprintf("metricsFor(%s,%s)", selector, info))
	names, err := helpers.ListObjectNames(p.mapper, p.client, namespace, selector, info)
	if err != nil {
		return nil, err
	}

	res := make([]custom_metrics.MetricValue, 0, len(names))
	for _, name := range names {
		namespacedName := types.NamespacedName{Name: name, Namespace: namespace}
		value, err := p.valueFor(info, namespacedName, selector, names, metricSelector)
		if err != nil {
			if apierr.IsNotFound(err) {
				continue
			}
			return nil, err
		}

		metric, err := p.metricFor(value, namespacedName, selector, info, metricSelector)
		if err != nil {
			return nil, err
		}
		res = append(res, *metric)
	}

	return &custom_metrics.MetricValueList{
		Items: res,
	}, nil
}

func (p *elasticsearchProvider) GetMetricByName(name types.NamespacedName, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValue, error) {
	klog.Infof("-> GetMetricByName(name=%v,info=%v,metricSelector=%v)", name, info, metricSelector)
	value, err := p.valueFor(info, name, labels.NewSelector(), []string{}, metricSelector)
	if err != nil {
		return nil, err
	}
	return p.metricFor(value, name, labels.Everything(), info, metricSelector)
}

func (p *elasticsearchProvider) GetMetricBySelector(namespace string, selector labels.Selector, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValueList, error) {
	klog.Infof("-> GetMetricBySelector(namespace=%v,selector=%v,info=%v,metricSelector=%v)", namespace, selector, info, metricSelector)
	return p.metricsFor(namespace, selector, info, metricSelector)
}

func (p *elasticsearchProvider) ListAllMetrics() []provider.CustomMetricInfo {
	klog.Info("-> ListAllMetrics()")
	allMetrics := p.metricLister.GetMetrics()
	klog.Infof("<- ListAllMetrics(), size: %d", len(allMetrics))
	for _, m := range allMetrics {
		klog.Infof(" - %s - %s", m.Metric, m.String())
	}
	return allMetrics
}

func (p *elasticsearchProvider) GetExternalMetric(_ string, _ labels.Selector, _ provider.ExternalMetricInfo) (*external_metrics.ExternalMetricValueList, error) {
	// TODO: Implement
	return nil, errors.New("GetExternalMetric not implemented")
}

func (p *elasticsearchProvider) ListAllExternalMetrics() []provider.ExternalMetricInfo {
	klog.Error("not implemented: ListAllExternalMetrics()")
	return []provider.ExternalMetricInfo{}
}
