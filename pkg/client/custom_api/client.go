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

package custom_api

import (
	"fmt"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/metrics/pkg/apis/custom_metrics"
	customMetricsAPI "k8s.io/metrics/pkg/apis/custom_metrics/v1beta2"
	"k8s.io/metrics/pkg/apis/external_metrics"
	externalMetricsAPI "k8s.io/metrics/pkg/apis/external_metrics/v1beta1"
	cmClient "k8s.io/metrics/pkg/client/custom_metrics"
	emClient "k8s.io/metrics/pkg/client/external_metrics"
	"sigs.k8s.io/custom-metrics-apiserver/pkg/provider"

	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/client"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/config"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/log"
)

var logger = log.ForPackage("custom_api")

type metricsClientProvider struct {
	baseConfig *rest.Config
	mapper     meta.RESTMapper
}

type metricsClient struct {
	metricServerCfg                  config.MetricServer
	customMetricsAvailableAPIsGetter cmClient.AvailableAPIsGetter
	customMetricsClient              cmClient.CustomMetricsClient

	externalMetricsClient emClient.ExternalMetricsClient
	discoveryClient       discovery.ServerResourcesInterface
	mapper                meta.RESTMapper

	rwLock                                 sync.RWMutex
	customMetricNamer, externalMetricNamer config.Namer
}

func (mc *metricsClient) GetConfiguration() config.MetricServer {
	return mc.metricServerCfg
}

func (mc *metricsClient) ListCustomMetricInfos() (map[provider.CustomMetricInfo]struct{}, error) {
	version, err := mc.customMetricsAvailableAPIsGetter.PreferredVersion()
	if err != nil {
		return nil, err
	}
	resources, err := mc.discoveryClient.ServerResourcesForGroupVersion(version.String())
	if err != nil {
		return nil, fmt.Errorf("failed to get resource for %s: %v", customMetricsAPI.SchemeGroupVersion, err)
	}
	metricInfos := make(map[provider.CustomMetricInfo]struct{})
	namer, err := config.NewNamer(mc.metricServerCfg.Rename)
	if err != nil {
		return nil, fmt.Errorf("%s: failed to create customMetricNamer: %v", mc.GetConfiguration().Name, err)
	}
	for _, r := range resources.APIResources {
		parts := strings.SplitN(r.Name, "/", 2)
		if len(parts) != 2 {
			logger.V(2).Info("provider returned a malformed metrics", "provider_name", mc.metricServerCfg.Name, "metric_name", r.Name)
			continue
		}
		info := provider.CustomMetricInfo{
			GroupResource: schema.ParseGroupResource(parts[0]),
			Namespaced:    r.Namespaced,
			Metric:        namer.Register(parts[1]),
		}
		metricInfos[info] = struct{}{}
	}
	mc.rwLock.Lock()
	defer mc.rwLock.Unlock()
	mc.customMetricNamer = namer
	return metricInfos, nil
}

func (mc *metricsClient) GetMetricByName(name types.NamespacedName, info provider.CustomMetricInfo, selector labels.Selector) (*custom_metrics.MetricValue, error) {
	mc.rwLock.Lock()
	defer mc.rwLock.Unlock()
	var object *customMetricsAPI.MetricValue
	var err error
	metricName, ok := mc.customMetricNamer.Get(info.Metric)
	if !ok {
		return nil, fmt.Errorf("metric name alias for custom metric %s not found", info.Metric)
	}
	if info.Namespaced {
		object, err = mc.customMetricsClient.NamespacedMetrics(name.Namespace).GetForObject(
			schema.GroupKind{Group: info.GroupResource.Group, Kind: info.GroupResource.Resource},
			name.Name, metricName, selector,
		)
	} else {
		object, err = mc.customMetricsClient.RootScopedMetrics().GetForObject(
			schema.GroupKind{Group: info.GroupResource.Group, Kind: info.GroupResource.Resource},
			name.Name, metricName, selector,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get metric from backend: %v", err)
	}
	return &custom_metrics.MetricValue{
		DescribedObject: custom_metrics.ObjectReference{
			Kind:            object.DescribedObject.Kind,
			Namespace:       object.DescribedObject.Namespace,
			Name:            object.DescribedObject.Name,
			APIVersion:      object.DescribedObject.APIVersion,
			ResourceVersion: object.DescribedObject.ResourceVersion,
		},
		Metric: custom_metrics.MetricIdentifier{
			Name:     object.Metric.Name,
			Selector: object.Metric.Selector,
		},
		Timestamp:     object.Timestamp,
		WindowSeconds: object.WindowSeconds,
		Value:         object.Value,
	}, nil
}

func (mc *metricsClient) GetMetricBySelector(namespace string, selector labels.Selector, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValueList, error) {
	var objects *customMetricsAPI.MetricValueList
	var err error
	kind, err := mc.mapper.ResourceSingularizer(info.GroupResource.Resource)
	if err != nil {
		return nil, fmt.Errorf("failed to singularize %s: %v", info.GroupResource.Resource, err)
	}
	logger.V(1).Info("GetMetricBySelector", "metric_info", info.String())
	mc.rwLock.Lock()
	defer mc.rwLock.Unlock()
	metricName, ok := mc.customMetricNamer.Get(info.Metric)
	if !ok {
		return nil, fmt.Errorf("metric name alias for custom metric %s/%s not found", namespace, info.Metric)
	}
	if info.Namespaced {
		objects, err = mc.customMetricsClient.NamespacedMetrics(namespace).GetForObjects(
			schema.GroupKind{
				Group: info.GroupResource.Group,
				Kind:  kind,
			},
			selector, metricName, metricSelector,
		)
	} else {
		objects, err = mc.customMetricsClient.RootScopedMetrics().GetForObjects(
			schema.GroupKind{
				Group: info.GroupResource.Group,
				Kind:  kind,
			},
			selector, metricName, metricSelector,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get metric from backend: %v", err)
	}
	values := make([]custom_metrics.MetricValue, len(objects.Items))
	for i, v := range objects.Items {
		values[i] = custom_metrics.MetricValue{
			DescribedObject: custom_metrics.ObjectReference{
				Kind:            v.DescribedObject.Kind,
				Namespace:       v.DescribedObject.Namespace,
				Name:            v.DescribedObject.Name,
				APIVersion:      v.DescribedObject.APIVersion,
				ResourceVersion: v.DescribedObject.ResourceVersion,
			},
			Metric: custom_metrics.MetricIdentifier{
				Name:     v.Metric.Name,
				Selector: v.Metric.Selector,
			},
			Timestamp:     v.Timestamp,
			WindowSeconds: v.WindowSeconds,
			Value:         v.Value,
		}
	}
	return &custom_metrics.MetricValueList{
		Items: values,
	}, nil
}

func (mc *metricsClient) ListExternalMetrics() (map[provider.ExternalMetricInfo]struct{}, error) {
	infos := make(map[provider.ExternalMetricInfo]struct{})
	resources, err := mc.discoveryClient.ServerResourcesForGroupVersion(externalMetricsAPI.SchemeGroupVersion.String())
	if err != nil {
		return nil, fmt.Errorf("failed to get resource for %s: %v", externalMetricsAPI.SchemeGroupVersion, err)
	}
	for _, r := range resources.APIResources {
		info := provider.ExternalMetricInfo{
			Metric: r.Name,
		}
		infos[info] = struct{}{}
	}
	return infos, nil
}

func (mc *metricsClient) GetExternalMetric(name, namespace string, selector labels.Selector) (*external_metrics.ExternalMetricValueList, error) {
	mc.rwLock.Lock()
	defer mc.rwLock.Unlock()
	metricName, ok := mc.externalMetricNamer.Get(name)
	if !ok {
		return nil, fmt.Errorf("metric name alias for external metric %s/%s not found", namespace, name)
	}
	result, err := mc.externalMetricsClient.NamespacedMetrics(namespace).List(metricName, selector)
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics for external metric %s/%s: %v", namespace, metricName, err)
	}
	valueList := &external_metrics.ExternalMetricValueList{
		Items: make([]external_metrics.ExternalMetricValue, len(result.Items)),
	}
	for i, m := range result.Items {
		valueList.Items[i] = external_metrics.ExternalMetricValue{
			TypeMeta:      metav1.TypeMeta{Kind: m.Kind, APIVersion: m.APIVersion},
			MetricName:    m.MetricName,
			MetricLabels:  m.MetricLabels,
			Timestamp:     m.Timestamp,
			WindowSeconds: m.WindowSeconds,
			Value:         m.Value,
		}
	}
	return valueList, nil
}

var _ client.Interface = &metricsClient{}

func NewMetricApiClientProvider(baseConfig *rest.Config, mapper meta.RESTMapper) *metricsClientProvider {
	return &metricsClientProvider{
		baseConfig: baseConfig,
		mapper:     mapper,
	}
}

func (mcp metricsClientProvider) NewClient(
	client *kubernetes.Clientset,
	metricServerCfg config.MetricServer,
) (client.Interface, error) {
	restClientConfig, err := metricServerCfg.ClientConfig.NewRestClientConfig(client, mcp.baseConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to generate rest restClientConfig for %s: %s", metricServerCfg.ClientConfig.Host, err)
	}
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(restClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client: %v", err)
	}
	customMetricsAvailableAPIsGetter := cmClient.NewAvailableAPIsGetter(discoveryClient)
	customMetricsClient := cmClient.NewForConfig(restClientConfig, mcp.mapper, customMetricsAvailableAPIsGetter)
	externalMetricsClient, err := emClient.NewForConfig(restClientConfig)
	if err != nil {
		return nil, fmt.Errorf("%s: failed to create external metrics client: %v", metricServerCfg.Name, err)
	}

	return &metricsClient{
		metricServerCfg:                  metricServerCfg,
		customMetricsAvailableAPIsGetter: customMetricsAvailableAPIsGetter,
		customMetricsClient:              customMetricsClient,
		externalMetricsClient:            externalMetricsClient,
		discoveryClient:                  discoveryClient,
		mapper:                           mcp.mapper,
	}, err
}
