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
	// registration is the handle for the event handler added below. Its
	// HasSynced reports true only once the initial AddFunc replay has been
	// delivered, unlike informer.HasSynced which only tracks the store.
	registration cache.ResourceEventHandlerRegistration
	// resyncPeriod is how often the informer does a full relist; it also re-delivers
	// every HPA, which drives the retry of any transiently-failed resolutions (see
	// retryUnresolved).
	resyncPeriod time.Duration

	// mu guards unresolved.
	mu sync.Mutex
	// unresolved holds the names of metrics that are referenced by at least one
	// HPA but are not advertised yet, mapped to the earliest time they may be
	// re-attempted. A transient Advertise error is retried on the next HPA event
	// (including the periodic resync). A "not found" answer is retried too, no
	// more often than notFoundRetryInterval: the field may appear later (dynamic
	// mapping on the first document, a new backing index), and the tracker
	// reports a name as added only once, so nothing else would re-probe it. In
	// steady state this set is empty.
	unresolved map[string]time.Time
	// notFoundRetryInterval bounds how often a not-found metric is re-probed,
	// so a name that stays unknown does not cost one _field_caps per HPA event
	// (the HPA controller updates each HPA's status every ~15s).
	notFoundRetryInterval time.Duration
}

// defaultNotFoundRetryInterval is the default Watcher.notFoundRetryInterval.
const defaultNotFoundRetryInterval = time.Minute

// NewWatcher builds a Watcher over the given clientset.
func NewWatcher(clientset kubernetes.Interface, registry MetricRegistry, resyncPeriod time.Duration) (*Watcher, error) {
	factory := informers.NewSharedInformerFactory(clientset, resyncPeriod)
	informer := factory.Autoscaling().V2().HorizontalPodAutoscalers().Informer()
	w := &Watcher{
		logger:       log.ForPackage("hpa-watcher"),
		registry:     registry,
		tracker:      newReferenceTracker(),
		factory:      factory,
		informer:     informer,
		resyncPeriod: resyncPeriod,
		unresolved:   make(map[string]time.Time),

		notFoundRetryInterval: defaultNotFoundRetryInterval,
	}
	registration, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    w.onUpsert,
		UpdateFunc: func(_, newObj interface{}) { w.onUpsert(newObj) },
		DeleteFunc: w.onDelete,
	})
	if err != nil {
		// Only fails once the informer has stopped, which cannot happen here,
		// but a nil registration would otherwise panic in Start.
		return nil, fmt.Errorf("HPA watcher: register event handler: %w", err)
	}
	w.registration = registration
	return w, nil
}

// Start launches the informer and blocks until the initial list of HPAs has
// been processed, so the caller can treat the registry as warm, or until
// syncTimeout elapses. ctx governs the informer's lifetime, not the wait.
//
// It waits on the event handler's registration rather than informer.HasSynced:
// the latter only reports that the store is populated, not that the initial
// AddFunc events have been delivered and each metric advertised. Waiting on the
// registration is what makes the cold-start guarantee real — the first scrape
// after Start returns cannot 404 on an already-referenced metric.
//
// The wait is bounded because the caller starts the API server only after
// Start returns, and an unbounded hang there is invisible: the monitoring
// server is already up. On timeout two cases are told apart. If the informer
// store itself never synced, the list/watch is failing (typically missing RBAC
// on horizontalpodautoscalers) and an error is returned so the caller exits
// loudly. If the store synced but the initial AddFunc replay is still running
// (each Advertise is a synchronous _field_caps call, so a hung Elasticsearch
// costs up to the per-metric timeout for every referenced name), Start returns
// nil: the API server can start and the remaining names are advertised as the
// replay completes.
func (w *Watcher) Start(ctx context.Context, syncTimeout time.Duration) error {
	w.logger.Info("Starting HPA watcher")
	w.factory.Start(ctx.Done())
	syncCtx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	if cache.WaitForCacheSync(syncCtx.Done(), w.registration.HasSynced) {
		w.logger.Info("HPA watcher cache synced")
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("HPA watcher: stopped before the informer cache synced: %w", ctx.Err())
	}
	if !w.informer.HasSynced() {
		return fmt.Errorf("HPA watcher: informer cache did not sync within %s; check that the adapter can list and watch horizontalpodautoscalers", syncTimeout)
	}
	w.logger.Info("HPA watcher: store synced but the initial advertise replay is still running; starting without waiting for it",
		"timeout", syncTimeout)
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
	// The tracker reports each name as "added" only once, so a failed or
	// not-found Advertise above would otherwise never be retried. Re-attempt any
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
// or a "not found" answer it records the name in the unresolved set so it is
// retried on a later HPA event; on success it clears the name.
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
		w.markUnresolved(name, time.Now())
	case !found:
		// Not served *now*. With dynamic mappings the field only exists once the
		// first document is indexed, and an HPA is often applied together with the
		// workload it scales, before that workload produces data. Keep probing at a
		// bounded rate so the metric is advertised once the field appears. Log the
		// first miss at Info and the retries at V(1) to keep a permanently
		// misnamed metric from flooding the log.
		//
		// Advertise only consults the registry's resolver clients (the
		// Elasticsearch clients). A metric served by another backend via periodic
		// discovery is invisible here, so scope the message to what was checked.
		first := w.markUnresolved(name, time.Now().Add(w.notFoundRetryInterval))
		msg := "HPA references a metric not found in any Elasticsearch metric set; will retry"
		if first {
			w.logger.Info(msg, "metric", name, "retry_after", w.notFoundRetryInterval)
		} else {
			w.logger.V(1).Info(msg, "metric", name, "retry_after", w.notFoundRetryInterval)
		}
	default:
		w.logger.Info("Advertised metric referenced by an HPA", "metric", name)
		w.clearUnresolved(name)
	}
}

func (w *Watcher) withdraw(names []string) {
	for _, name := range names {
		w.registry.Withdraw(name)
		w.clearUnresolved(name)
		w.logger.Info("Withdrew metric no longer referenced by any HPA", "metric", name)
	}
}

// markUnresolved adds a metric name to the retry set with the earliest time it
// may be re-attempted. It reports whether the name was not in the set before.
func (w *Watcher) markUnresolved(name string, notBefore time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, known := w.unresolved[name]
	w.unresolved[name] = notBefore
	return !known
}

// clearUnresolved removes a metric name from the retry set.
func (w *Watcher) clearUnresolved(name string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.unresolved, name)
}

// retryUnresolved re-attempts to advertise every metric in the retry set whose
// earliest retry time has passed. It is a no-op in steady state, when the set
// is empty.
func (w *Watcher) retryUnresolved() {
	w.mu.Lock()
	if len(w.unresolved) == 0 {
		w.mu.Unlock()
		return
	}
	now := time.Now()
	names := make([]string, 0, len(w.unresolved))
	for name, notBefore := range w.unresolved {
		if !now.Before(notBefore) {
			names = append(names, name)
		}
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
