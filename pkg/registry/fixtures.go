/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package registry

import (
	"sync"

	"github.com/elastic/elasticsearch-adapter/pkg/client"
	"github.com/elastic/elasticsearch-adapter/pkg/config"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/metrics/pkg/apis/custom_metrics"
	"k8s.io/metrics/pkg/apis/external_metrics"
	"sigs.k8s.io/custom-metrics-apiserver/pkg/provider"
)

type fakeMetricsClient struct {
	config.MetricServer
	customMetrics   map[provider.CustomMetricInfo]struct{}
	externalMetrics map[provider.ExternalMetricInfo]struct{}
}

var _ client.Interface = &fakeMetricsClient{}

func newFakeMetricsClient(name string, priority int) *fakeMetricsClient {
	return &fakeMetricsClient{
		MetricServer: config.MetricServer{
			Name:     name,
			Priority: priority,
		},
		customMetrics:   make(map[provider.CustomMetricInfo]struct{}),
		externalMetrics: make(map[provider.ExternalMetricInfo]struct{}),
	}
}

func (fmc *fakeMetricsClient) GetConfiguration() config.MetricServer {
	return fmc.MetricServer
}

func (fmc *fakeMetricsClient) GetMetricByName(types.NamespacedName, provider.CustomMetricInfo, labels.Selector) (*custom_metrics.MetricValue, error) {
	panic("not implemented")
}

func (fmc *fakeMetricsClient) GetMetricBySelector(string, labels.Selector, provider.CustomMetricInfo, labels.Selector) (*custom_metrics.MetricValueList, error) {
	panic("not implemented")
}

func (fmc *fakeMetricsClient) GetExternalMetric(string, string, labels.Selector) (*external_metrics.ExternalMetricValueList, error) {
	panic("not implemented")
}

func (fmc *fakeMetricsClient) ListCustomMetricInfos() (map[provider.CustomMetricInfo]struct{}, error) {
	return fmc.customMetrics, nil
}

func (fmc *fakeMetricsClient) ListExternalMetrics() (map[provider.ExternalMetricInfo]struct{}, error) {
	return fmc.externalMetrics, nil
}

type fakeRegistry struct {
	registry *Registry
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{
		registry: &Registry{
			lock:            sync.RWMutex{},
			customMetrics:   make(map[provider.CustomMetricInfo]*metricClients),
			externalMetrics: make(map[provider.ExternalMetricInfo]*metricClients),
		},
	}
}

// addExistingCustomMetrics adds some pre-existing custom metrics in the registry
func (f *fakeRegistry) addExistingCustomMetrics(metricClient client.Interface, metricsName ...string) *fakeRegistry {
	for _, metricName := range metricsName {
		metricInfo := provider.CustomMetricInfo{Metric: metricName}
		cachedMetricClients := f.registry.customMetrics[metricInfo]
		if cachedMetricClients == nil {
			cachedMetricClients = newMetricClients()
		}
		f.registry.customMetrics[metricInfo] = ptr(append(*cachedMetricClients, metricClient))
	}
	return f
}

// addExternalCustomMetrics adds some pre-existing external metrics in the registry
func (f *fakeRegistry) addExternalCustomMetrics(metricClient client.Interface, metricsName ...string) *fakeRegistry {
	for _, metricName := range metricsName {
		metricInfo := provider.ExternalMetricInfo{Metric: metricName}
		cachedMetricClients := f.registry.externalMetrics[metricInfo]
		if cachedMetricClients == nil {
			cachedMetricClients = newMetricClients()
		}
		f.registry.externalMetrics[metricInfo] = ptr(append(*cachedMetricClients, metricClient))
	}
	return f
}

func ptr(c metricClients) *metricClients {
	return &c
}

func fakeCustomMetricSet(names ...string) map[provider.CustomMetricInfo]struct{} {
	result := make(map[provider.CustomMetricInfo]struct{}, len(names))
	for _, name := range names {
		result[provider.CustomMetricInfo{
			GroupResource: schema.GroupResource{},
			Namespaced:    false,
			Metric:        name,
		}] = struct{}{}
	}
	return result
}

func fakeExternalMetricSet(names ...string) map[provider.ExternalMetricInfo]struct{} {
	result := make(map[provider.ExternalMetricInfo]struct{}, len(names))
	for _, name := range names {
		result[provider.ExternalMetricInfo{
			Metric: name,
		}] = struct{}{}
	}
	return result
}
