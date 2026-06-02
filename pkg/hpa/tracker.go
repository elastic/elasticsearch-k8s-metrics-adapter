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

package hpa

import (
	"sync"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
)

// metricNames extracts the custom-metric names referenced by an HPA. Only
// metric types served through the custom metrics API are considered: Pods and
// Object. Resource / ContainerResource (cpu, memory) are served by the built-in
// metrics API and External metrics go through a different path, so both are
// ignored here.
func metricNames(hpa *autoscalingv2.HorizontalPodAutoscaler) []string {
	if hpa == nil {
		return nil
	}
	var names []string
	for _, m := range hpa.Spec.Metrics {
		switch m.Type {
		case autoscalingv2.PodsMetricSourceType:
			if m.Pods != nil {
				names = append(names, m.Pods.Metric.Name)
			}
		case autoscalingv2.ObjectMetricSourceType:
			if m.Object != nil {
				names = append(names, m.Object.Metric.Name)
			}
		}
	}
	return names
}

// referenceTracker maintains, for each HPA, the set of custom-metric names it
// references, and a reference count per metric name. It reports when a metric
// name becomes referenced by the first HPA (added) and when it is no longer
// referenced by any HPA (removed), so the caller can advertise / withdraw it.
//
// All methods are safe for concurrent use.
type referenceTracker struct {
	mu       sync.Mutex
	byHPA    map[string]map[string]struct{} // hpa key → set of metric names
	refCount map[string]int                 // metric name → number of HPAs referencing it
}

func newReferenceTracker() *referenceTracker {
	return &referenceTracker{
		byHPA:    make(map[string]map[string]struct{}),
		refCount: make(map[string]int),
	}
}

// upsert records that the HPA identified by key references exactly the given
// metric names (replacing any previously recorded set). It returns the names
// that just became referenced for the first time (added) and the names that are
// no longer referenced by any HPA (removed).
func (t *referenceTracker) upsert(key string, names []string) (added, removed []string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	newSet := make(map[string]struct{}, len(names))
	for _, n := range names {
		newSet[n] = struct{}{}
	}
	oldSet := t.byHPA[key]

	// Names added by this HPA.
	for n := range newSet {
		if _, had := oldSet[n]; had {
			continue
		}
		if t.refCount[n] == 0 {
			added = append(added, n)
		}
		t.refCount[n]++
	}
	// Names dropped by this HPA.
	for n := range oldSet {
		if _, still := newSet[n]; still {
			continue
		}
		t.refCount[n]--
		if t.refCount[n] <= 0 {
			delete(t.refCount, n)
			removed = append(removed, n)
		}
	}

	if len(newSet) == 0 {
		delete(t.byHPA, key)
	} else {
		t.byHPA[key] = newSet
	}
	return added, removed
}

// remove drops all references held by the HPA identified by key. It returns the
// names that are no longer referenced by any HPA.
func (t *referenceTracker) remove(key string) (removed []string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	oldSet := t.byHPA[key]
	for n := range oldSet {
		t.refCount[n]--
		if t.refCount[n] <= 0 {
			delete(t.refCount, n)
			removed = append(removed, n)
		}
	}
	delete(t.byHPA, key)
	return removed
}
