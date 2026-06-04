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

package registry

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/custom-metrics-apiserver/pkg/provider"

	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/client"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/log"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/scheduler"
)

// Registry is the central routing table of the adapter.
//
// It has two responsibilities:
//
//  1. Advertisement — it answers the Kubernetes custom metrics API's
//     ListAllCustomMetrics / ListAllExternalMetrics calls.  The K8s API server
//     uses these lists to build its own resource catalogue; a metric that is
//     not listed here will get a 404 from the API server before the request
//     ever reaches the adapter.
//
//  2. Routing — when an HPA requests the value of metric X, the registry
//     returns the backend client (e.g. the Elasticsearch client) responsible
//     for fetching it.
//
// Both responsibilities are served by the same map (customMetrics / externalMetrics):
// the keys are what get listed; the values are the clients used for routing.
//
// Metrics are added to the registry in two ways:
//   - Periodically, via UpdateCustomMetrics / UpdateExternalMetrics, called by
//     the scheduler after each discovery cycle (used by custom_api clients).
//   - Proactively, via Advertise, called by the HPA watcher when it sees a new
//     metric referenced by an HPA (used by Elasticsearch clients in hpa mode).
type Registry struct {
	logger logr.Logger
	lock   sync.RWMutex

	// customMetrics is simultaneously the advertisement catalogue
	// (ListAllCustomMetrics iterates it) and the routing table
	// (GetCustomMetricClient looks up in it).
	customMetrics   map[provider.CustomMetricInfo]*metricClients
	externalMetrics map[provider.ExternalMetricInfo]*metricClients

	// advertisedByName indexes metrics registered via Advertise by their plain
	// metric name so they can be withdrawn by name when no HPA references them.
	advertisedByName map[string]provider.CustomMetricInfo

	// resolver performs on-demand _field_caps lookups for individual metric
	// names. Set in hpa discovery mode; nil in periodic mode.
	resolver *Resolver
}

func NewRegistry() *Registry {
	return &Registry{
		logger:           log.ForPackage("registry"),
		lock:             sync.RWMutex{},
		customMetrics:    make(map[provider.CustomMetricInfo]*metricClients),
		externalMetrics:  make(map[provider.ExternalMetricInfo]*metricClients),
		advertisedByName: make(map[string]provider.CustomMetricInfo),
	}
}

// WithResolver attaches a Resolver used by Advertise to perform _field_caps
// lookups when the HPA watcher discovers a new metric name.
func (r *Registry) WithResolver(resolver *Resolver) *Registry {
	r.resolver = resolver
	return r
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

// GetCustomMetricClient returns the backend client that can fetch the value of
// the given custom metric. It is called on every HPA reconcile. Returns a 404
// StatusError if the metric is not in the registry (i.e. was not advertised).
func (r *Registry) GetCustomMetricClient(info provider.CustomMetricInfo) (client.Interface, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	metricClients, ok := r.customMetrics[info]
	if !ok {
		r.logger.V(1).Info("Custom metric is not served by any metric client", "metric_name", info.Metric)
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
		return nil, fmt.Errorf("no backend for custom metric: %v", info.Metric)
	}
	r.logger.V(1).Info(
		"Custom metric found", "metric", info.String(),
		"client_name", metricClient.GetConfiguration().Name,
		"client_host", metricClient.GetConfiguration().ClientConfig.Host,
	)
	return metricClient, nil
}

// registerResolved writes a successfully resolved metric into both the routing
// table (customMetrics) and the name index (advertisedByName). Writing to
// customMetrics is what makes the metric appear in ListAllCustomMetrics, which
// is what the K8s API server needs before it will route requests for this metric
// to the adapter.
func (r *Registry) registerResolved(entry *ResolvedEntry) {
	r.lock.Lock()
	defer r.lock.Unlock()
	if _, exists := r.customMetrics[entry.Info]; !exists {
		r.customMetrics[entry.Info] = newMetricClients()
	}
	r.customMetrics[entry.Info].addOrUpdateClient(entry.Client)
	r.advertisedByName[entry.Info.Metric] = entry.Info
}

// Advertise proactively resolves a metric by name and registers it so it is
// served and listed by ListAllCustomMetrics. Used by HPA-driven discovery to
// populate the registry before the first HPA reconcile arrives.
//
// Returns true if the metric was resolved and advertised, false if no client
// serves it. A non-nil error indicates a transient resolution failure.
func (r *Registry) Advertise(ctx context.Context, metricName string) (bool, error) {
	if r.resolver == nil {
		return false, fmt.Errorf("no resolver configured")
	}
	entry, err := r.resolver.Resolve(ctx, metricName)
	if err != nil {
		return false, err
	}
	if entry == nil {
		return false, nil
	}
	r.registerResolved(entry)
	r.logger.V(1).Info(
		"Custom metric advertised", "metric", entry.Info.String(),
		"client_name", entry.Client.GetConfiguration().Name,
	)
	return true, nil
}

// Withdraw removes a previously advertised metric from the served set. It is a
// no-op if the metric was not advertised. Used when no HPA references the
// metric any more.
func (r *Registry) Withdraw(metricName string) {
	r.lock.Lock()
	defer r.lock.Unlock()
	info, ok := r.advertisedByName[metricName]
	if !ok {
		return
	}
	delete(r.customMetrics, info)
	delete(r.advertisedByName, metricName)
	r.logger.V(1).Info("Custom metric withdrawn", "metric", metricName)
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
	r.logger.V(1).Info(
		"External metric found", "metric", info.Metric,
		"client_name", metricClient.GetConfiguration().Name,
		"client_host", metricClient.GetConfiguration().ClientConfig.Host,
	)
	return metricClient, nil
}

// ListAllCustomMetrics returns every metric currently in the registry.
// The K8s API server polls this to build the list of routes it will forward to
// the adapter; a metric absent from this list gets a 404 before reaching us.
func (r *Registry) ListAllCustomMetrics() []provider.CustomMetricInfo {
	r.lock.RLock()
	defer r.lock.RUnlock()
	infos := make([]provider.CustomMetricInfo, len(r.customMetrics))
	count := 0
	for k := range r.customMetrics {
		infos[count] = k
		count++
	}
	r.logger.V(1).Info("Custom metrics served by the adapter", "count", len(infos))
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
	r.logger.V(1).Info("External metrics served by the adapter", "count", len(infos))
	return infos
}
