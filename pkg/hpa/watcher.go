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

// Package hpa implements HPA-driven custom metric discovery: it watches
// HorizontalPodAutoscaler objects to learn which custom metrics are actually
// needed, and asks the registry to advertise (resolve via _field_caps) only
// those, instead of discovering every field in the Elasticsearch mapping.
package hpa

import (
	"context"
	"time"

	"github.com/go-logr/logr"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/log"
)

// MetricRegistry is the subset of the registry the watcher depends on.
type MetricRegistry interface {
	// Advertise resolves the named metric and registers it so it is served and
	// listed. Returns false if no client serves it.
	Advertise(ctx context.Context, metricName string) (bool, error)
	// Withdraw removes a previously advertised metric.
	Withdraw(metricName string)
}

// Watcher watches HPAs and keeps the registry's advertised custom metrics in
// sync with the metrics those HPAs reference.
type Watcher struct {
	logger   logr.Logger
	registry MetricRegistry
	tracker  *referenceTracker
	factory  informers.SharedInformerFactory
	informer cache.SharedIndexInformer
	// resyncPeriod is how often the informer does a full relist; it also drives
	// periodic re-Advertise so transiently-failed resolutions get retried.
	resyncPeriod time.Duration
}

// NewWatcher builds a Watcher over the given clientset.
func NewWatcher(clientset kubernetes.Interface, registry MetricRegistry, resyncPeriod time.Duration) *Watcher {
	factory := informers.NewSharedInformerFactory(clientset, resyncPeriod)
	informer := factory.Autoscaling().V2().HorizontalPodAutoscalers().Informer()
	w := &Watcher{
		logger:       log.ForPackage("hpa-watcher"),
		registry:     registry,
		tracker:      newReferenceTracker(),
		factory:      factory,
		informer:     informer,
		resyncPeriod: resyncPeriod,
	}
	_, _ = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    w.onUpsert,
		UpdateFunc: func(_, newObj interface{}) { w.onUpsert(newObj) },
		DeleteFunc: w.onDelete,
	})
	return w
}

// Start launches the informer and blocks until the cache has synced or the
// context is cancelled. It returns once the initial list of HPAs has been
// processed, so the caller can treat the registry as warm.
func (w *Watcher) Start(ctx context.Context) error {
	w.logger.Info("Starting HPA watcher")
	w.factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), w.informer.HasSynced) {
		return context.Canceled
	}
	w.logger.Info("HPA watcher cache synced")
	return nil
}

func (w *Watcher) onUpsert(obj interface{}) {
	hpa, ok := toHPA(obj)
	if !ok {
		return
	}
	key, err := cache.MetaNamespaceKeyFunc(hpa)
	if err != nil {
		w.logger.Error(err, "failed to derive HPA key")
		return
	}
	added, removed := w.tracker.upsert(key, metricNames(hpa))
	w.advertise(added)
	w.withdraw(removed)
}

func (w *Watcher) onDelete(obj interface{}) {
	// Object may be wrapped in a DeletedFinalStateUnknown tombstone.
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	hpa, ok := toHPA(obj)
	if !ok {
		return
	}
	key, err := cache.MetaNamespaceKeyFunc(hpa)
	if err != nil {
		w.logger.Error(err, "failed to derive HPA key")
		return
	}
	w.withdraw(w.tracker.remove(key))
}

func (w *Watcher) advertise(names []string) {
	for _, name := range names {
		// Best-effort, bounded resolution per metric. A transient failure here
		// is retried on the next informer resync.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		found, err := w.registry.Advertise(ctx, name)
		cancel()
		switch {
		case err != nil:
			w.logger.Error(err, "failed to advertise metric referenced by an HPA", "metric", name)
		case !found:
			w.logger.Info("HPA references a metric not served by any client", "metric", name)
		default:
			w.logger.V(1).Info("Advertised metric referenced by an HPA", "metric", name)
		}
	}
}

func (w *Watcher) withdraw(names []string) {
	for _, name := range names {
		w.registry.Withdraw(name)
		w.logger.V(1).Info("Withdrew metric no longer referenced by any HPA", "metric", name)
	}
}

func toHPA(obj interface{}) (*autoscalingv2.HorizontalPodAutoscaler, bool) {
	hpa, ok := obj.(*autoscalingv2.HorizontalPodAutoscaler)
	return hpa, ok
}
