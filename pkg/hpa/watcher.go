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
	"fmt"
	"sync"
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
	// resyncPeriod is how often the informer does a full relist; it also re-delivers
	// every HPA, which drives the retry of any transiently-failed resolutions (see
	// retryUnresolved).
	resyncPeriod time.Duration

	// mu guards unresolved.
	mu sync.Mutex
	// unresolved holds the names of metrics that are referenced by at least one
	// HPA but whose Advertise call failed transiently. They are retried on every
	// subsequent HPA event (including the periodic resync). In steady state this
	// set is empty.
	unresolved map[string]struct{}
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
		unresolved:   make(map[string]struct{}),
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
		return fmt.Errorf("HPA watcher: informer cache failed to sync")
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
	names := metricNames(hpa)
	w.logger.V(1).Info("Observed HPA", "hpa", key, "custom_metrics", names)
	added, removed := w.tracker.upsert(key, names)
	w.advertise(added)
	w.withdraw(removed)
	// The tracker reports each name as "added" only once, so a transient
	// Advertise failure above would otherwise never be retried. Re-attempt any
	// still-unresolved names now; HPA status updates and the informer's periodic
	// resync re-deliver the object, so this provides the retry tick.
	w.retryUnresolved()
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
		w.advertiseOne(name)
	}
}

// advertiseOne resolves and advertises a single metric. On a transient failure
// it records the name in the unresolved set so it is retried on a later HPA
// event; on success or a definitive "not served" answer it clears the name.
func (w *Watcher) advertiseOne(name string) {
	// This resolve runs synchronously on the informer's handler goroutine, so a
	// burst of newly-referenced metrics is processed one at a time and other HPA
	// events queue behind it. That is acceptable: the set of distinct referenced
	// metrics is small and each resolve is a single tiny _field_caps call. The
	// per-metric timeout bounds the worst case (a hung Elasticsearch) so one bad
	// metric cannot stall the handler indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	found, err := w.registry.Advertise(ctx, name)
	cancel()
	switch {
	case err != nil:
		w.logger.Error(err, "Failed to advertise metric referenced by an HPA; will retry", "metric", name)
		w.setUnresolved(name, true)
	case !found:
		w.logger.Info("HPA references a metric not served by any client", "metric", name)
		w.setUnresolved(name, false)
	default:
		w.logger.Info("Advertised metric referenced by an HPA", "metric", name)
		w.setUnresolved(name, false)
	}
}

func (w *Watcher) withdraw(names []string) {
	for _, name := range names {
		w.registry.Withdraw(name)
		w.setUnresolved(name, false)
		w.logger.Info("Withdrew metric no longer referenced by any HPA", "metric", name)
	}
}

// setUnresolved adds (unresolved=true) or removes (unresolved=false) a metric
// name from the retry set.
func (w *Watcher) setUnresolved(name string, unresolved bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if unresolved {
		w.unresolved[name] = struct{}{}
	} else {
		delete(w.unresolved, name)
	}
}

// retryUnresolved re-attempts to advertise every metric currently in the retry
// set. It is a no-op in steady state, when the set is empty.
func (w *Watcher) retryUnresolved() {
	w.mu.Lock()
	if len(w.unresolved) == 0 {
		w.mu.Unlock()
		return
	}
	names := make([]string, 0, len(w.unresolved))
	for name := range w.unresolved {
		names = append(names, name)
	}
	w.mu.Unlock()
	for _, name := range names {
		w.advertiseOne(name)
	}
}

func toHPA(obj interface{}) (*autoscalingv2.HorizontalPodAutoscaler, bool) {
	hpa, ok := obj.(*autoscalingv2.HorizontalPodAutoscaler)
	return hpa, ok
}
