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

package upstream

import (
	"errors"

	"github.com/elastic/elasticsearch-adapter/pkg/common"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"k8s.io/metrics/pkg/apis/custom_metrics"
	"k8s.io/metrics/pkg/apis/custom_metrics/v1beta2"
	"k8s.io/metrics/pkg/apis/external_metrics"
	custom_metrics_client "k8s.io/metrics/pkg/client/custom_metrics"

	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider/helpers"
)

type upstreamProvider struct {
	common.MetricLister
	mapper         apimeta.RESTMapper
	upstreamClient custom_metrics_client.CustomMetricsClient
}

// NewUpstreamProvider returns an instance of the upstream provider, along with its restful.WebService that opens endpoints to post custom metrics stored in Elasticsearch.
func NewUpstreamProvider(
	mapper apimeta.RESTMapper,
	upstreamClient custom_metrics_client.CustomMetricsClient,
	metricLister common.MetricLister,
) *upstreamProvider {
	return &upstreamProvider{
		MetricLister:   metricLister,
		mapper:         mapper,
		upstreamClient: upstreamClient,
	}
}

// metricFor is a helper function which formats a value, metric, and object info into a MetricValue which can be returned by the metrics API
func (p *upstreamProvider) metricFor(
	timeStampedMetric common.TimestampedMetric,
	name types.NamespacedName,
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
		Timestamp: timeStampedMetric.Timestamp,
		Value:     timeStampedMetric.Value,
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
func (p *upstreamProvider) metricsFor(
	info provider.CustomMetricInfo,
	metricSelector labels.Selector,
	metricValueList *v1beta2.MetricValueList,
) (*custom_metrics.MetricValueList, error) {

	res := make([]custom_metrics.MetricValue, 0, len(metricValueList.Items))
	for _, metricValue := range metricValueList.Items {
		object := metricValue.DescribedObject
		namespacedName := types.NamespacedName{
			Name:      object.Name,
			Namespace: object.Namespace,
		}
		timestampedMetric := common.TimestampedMetric{
			Value:     metricValue.Value,
			Timestamp: metricValue.Timestamp,
		}
		metric, err := p.metricFor(timestampedMetric, namespacedName, info, metricSelector)
		if err != nil {
			return nil, err
		}
		res = append(res, *metric)
	}

	return &custom_metrics.MetricValueList{
		Items: res,
	}, nil
}

func (p *upstreamProvider) GetMetricByName(name types.NamespacedName, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValue, error) {
	klog.Infof("upstream.GetMetricByName(name=%v,info=%v,metricSelector=%v)", name, info, metricSelector)
	gk, err := p.mapper.KindFor(info.GroupResource.WithVersion(""))
	if err != nil {
		return nil, err
	}
	if info.Namespaced {
		object, err := p.upstreamClient.NamespacedMetrics(name.Namespace).GetForObject(gk.GroupKind(), name.Name, info.Metric, metricSelector)
		if err != nil {
			return nil, err
		}
		return p.metricFor(common.TimestampedMetric{
			Value:     object.Value,
			Timestamp: object.Timestamp,
		}, name, info, metricSelector)
	}

	// Not namespaced
	object, err := p.upstreamClient.RootScopedMetrics().GetForObject(gk.GroupKind(), name.Name, info.Metric, metricSelector)
	if err != nil {
		return nil, err
	}
	return p.metricFor(common.TimestampedMetric{
		Value:     object.Value,
		Timestamp: object.Timestamp,
	}, name, info, metricSelector)
}

func (p *upstreamProvider) GetMetricBySelector(namespace string, selector labels.Selector, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValueList, error) {
	klog.Infof("upstream.GetMetricBySelector(namespace=%v,selector=%v,info=%v,metricSelector=%v)", namespace, selector, info, metricSelector)
	gk, err := p.mapper.KindFor(info.GroupResource.WithVersion(""))
	if err != nil {
		return nil, err
	}
	if info.Namespaced {
		objects, err := p.upstreamClient.NamespacedMetrics(namespace).GetForObjects(gk.GroupKind(), selector, info.Metric, metricSelector)
		if err != nil {
			return nil, err
		}
		return p.metricsFor(info, metricSelector, objects)
	}

	// Not namespaced
	objects, err := p.upstreamClient.RootScopedMetrics().GetForObjects(gk.GroupKind(), selector, info.Metric, metricSelector)
	if err != nil {
		return nil, err
	}
	return p.metricsFor(info, metricSelector, objects)
}

func (p *upstreamProvider) GetExternalMetric(_ string, _ labels.Selector, _ provider.ExternalMetricInfo) (*external_metrics.ExternalMetricValueList, error) {
	// TODO: Implement
	return nil, errors.New("GetExternalMetric not implemented")
}

func (p *upstreamProvider) ListAllExternalMetrics() []provider.ExternalMetricInfo {
	klog.Error("not implemented: ListAllExternalMetrics()")
	return []provider.ExternalMetricInfo{}
}
