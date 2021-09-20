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
	"fmt"
	"net/http"
	"sync"

	"github.com/elastic/elasticsearch-adapter/pkg/client"
	"github.com/elastic/elasticsearch-adapter/pkg/scheduler"
	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

// Registry maintains a list of the available metrics and the associated metrics sources.
// The aim of the registry is to cache the metrics lists as they can be expensive to retrieve and compute from
// the various sources.
type Registry struct {
	lock sync.RWMutex

	customMetrics   map[provider.CustomMetricInfo]*metricClients
	externalMetrics map[provider.ExternalMetricInfo]*metricClients
}

func NewRegistry() *Registry {
	return &Registry{
		lock:            sync.RWMutex{},
		customMetrics:   make(map[provider.CustomMetricInfo]*metricClients),
		externalMetrics: make(map[provider.ExternalMetricInfo]*metricClients),
	}
}

// getCustomMetricsFrom builds a set of the custom metrics currently served by a client.
func (r *Registry) getCustomMetricsFrom(clientName string) map[provider.CustomMetricInfo]struct{} {
	cms := make(map[provider.CustomMetricInfo]struct{})
	for cm, clients := range r.customMetrics {
		if clients == nil {
			// should not happen, but still want to be on the safe side
			continue
		}
		for _, c := range *clients {
			if c.GetConfiguration().Name == clientName {
				cm := cm
				cms[cm] = struct{}{}
			}
		}
	}
	return cms
}

// getExternalMetricsFrom builds a set of the external metrics currently served by a client.
func (r *Registry) getExternalMetricsFrom(clientName string) map[provider.ExternalMetricInfo]struct{} {
	ems := make(map[provider.ExternalMetricInfo]struct{})
	for em, clients := range r.externalMetrics {
		if clients == nil {
			// should not happen, but we want to be on the safe side
			continue
		}
		for _, c := range *clients {
			if c.GetConfiguration().Name == clientName {
				em := em
				ems[em] = struct{}{}
			}
		}
	}
	return ems
}

var _ scheduler.MetricListener = &Registry{}

func (r *Registry) OnError(err error) {}

func (r *Registry) UpdateCustomMetrics(
	metricClient client.Interface,
	cms map[provider.CustomMetricInfo]struct{},
) {
	r.lock.Lock()
	defer r.lock.Unlock()

	clientName := metricClient.GetConfiguration().Name
	actualCustomMetrics := r.getCustomMetricsFrom(clientName)
	// Check custom metrics that are no longer served.
	removedCustomMetrics := getRemovedCustomMetrics(actualCustomMetrics, cms)
	for _, removedMetric := range removedCustomMetrics {
		// This custom metric is no longer served by the client.
		clients := r.customMetrics[removedMetric]
		if empty := clients.removeClient(clientName); empty {
			delete(r.customMetrics, removedMetric)
		}
	}

	for mInfo := range cms {
		var ok bool
		if _, ok = r.customMetrics[mInfo]; !ok {
			r.customMetrics[mInfo] = newMetricClients()
		}
		serviceList := r.customMetrics[mInfo]
		serviceList.addOrUpdateClient(metricClient)
	}
}

func (r *Registry) UpdateExternalMetrics(
	metricClient client.Interface,
	ems map[provider.ExternalMetricInfo]struct{},
) {
	r.lock.Lock()
	defer r.lock.Unlock()

	clientName := metricClient.GetConfiguration().Name
	actualExternalMetrics := r.getExternalMetricsFrom(clientName)
	// Check external metrics that are no longer served.
	removedExternalMetrics := getRemovedExternalMetrics(actualExternalMetrics, ems)
	for _, removedMetric := range removedExternalMetrics {
		// This external metric is no longer served by the client.
		clients := r.externalMetrics[removedMetric]
		if empty := clients.removeClient(clientName); empty {
			delete(r.externalMetrics, removedMetric)
		}
	}

	for eInfo := range ems {
		var ok bool
		if _, ok = r.externalMetrics[eInfo]; !ok {
			r.externalMetrics[eInfo] = newMetricClients()
		}
		serviceList := r.externalMetrics[eInfo]
		serviceList.addOrUpdateClient(metricClient)
	}
}

func getRemovedCustomMetrics(old map[provider.CustomMetricInfo]struct{}, new map[provider.CustomMetricInfo]struct{}) []provider.CustomMetricInfo {
	var outdated []provider.CustomMetricInfo
	for info := range old {
		if _, ok := new[info]; !ok {
			outdated = append(outdated, info)
		}
	}
	return outdated
}

func getRemovedExternalMetrics(old map[provider.ExternalMetricInfo]struct{}, new map[provider.ExternalMetricInfo]struct{}) []provider.ExternalMetricInfo {
	var outdated []provider.ExternalMetricInfo
	for info := range old {
		if _, ok := new[info]; !ok {
			outdated = append(outdated, info)
		}
	}
	return outdated
}

func (r *Registry) GetCustomMetricClient(info provider.CustomMetricInfo) (client.Interface, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	var metricClients *metricClients
	var ok bool
	if metricClients, ok = r.customMetrics[info]; !ok {
		return nil, &errors.StatusError{
			ErrStatus: metav1.Status{
				Status:  metav1.StatusFailure,
				Code:    http.StatusNotFound,
				Reason:  metav1.StatusReasonNotFound,
				Message: fmt.Sprintf("custom metric %s is not served by any metric client", info.Metric),
			}}
	}
	metricClient, err := metricClients.getBestMetricClient()
	if err != nil {
		return nil, fmt.Errorf("not backend for metric: %v", info.Metric)
	}
	klog.V(1).Infof(
		"custom metric %v served by %s / %s", info,
		metricClient.GetConfiguration().Name,
		metricClient.GetConfiguration().ClientConfig.Host,
	)
	return metricClient, nil
}

func (r *Registry) GetExternalMetricClient(info provider.ExternalMetricInfo) (client.Interface, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	var metricClients *metricClients
	var ok bool
	if metricClients, ok = r.externalMetrics[info]; !ok {
		return nil, &errors.StatusError{
			ErrStatus: metav1.Status{
				Status:  metav1.StatusFailure,
				Code:    http.StatusNotFound,
				Reason:  metav1.StatusReasonNotFound,
				Message: fmt.Sprintf("external metric %s is not served by any metric client", info.Metric),
			}}
	}
	metricClient, err := metricClients.getBestMetricClient()
	if err != nil {
		return nil, fmt.Errorf("not backend for metric: %v", info.Metric)
	}
	klog.V(1).Infof(
		"external metric %v served by %s / %s", info,
		metricClient.GetConfiguration().Name,
		metricClient.GetConfiguration().ClientConfig.Host,
	)
	return metricClient, nil
}

func (r *Registry) ListAllCustomMetrics() []provider.CustomMetricInfo {
	r.lock.RLock()
	defer r.lock.RUnlock()
	infos := make([]provider.CustomMetricInfo, len(r.customMetrics))
	count := 0
	for k := range r.customMetrics {
		infos[count] = k
		count++
	}
	return infos
}

func (r *Registry) ListAllExternalMetrics() []provider.ExternalMetricInfo {
	r.lock.RLock()
	defer r.lock.RUnlock()
	infos := make([]provider.ExternalMetricInfo, len(r.externalMetrics))
	count := 0
	for k := range r.externalMetrics {
		infos[count] = k
		count++
	}
	return infos
}
