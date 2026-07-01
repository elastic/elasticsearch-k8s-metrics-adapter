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
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// fakeRegistry records advertise/withdraw calls.
type fakeRegistry struct {
	mu         sync.Mutex
	advertised map[string]int // successful advertises, keyed by metric name
	attempts   map[string]int // total advertise attempts, keyed by metric name
	withdrawn  map[string]int
	// failTimes is the number of initial advertise attempts that return a
	// transient error before succeeding.
	failTimes int
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{
		advertised: make(map[string]int),
		attempts:   make(map[string]int),
		withdrawn:  make(map[string]int),
	}
}

func (f *fakeRegistry) Advertise(_ context.Context, metricName string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts[metricName]++
	if f.failTimes > 0 {
		f.failTimes--
		return false, errors.New("transient resolve failure")
	}
	f.advertised[metricName]++
	return true, nil
}

func (f *fakeRegistry) Withdraw(metricName string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.withdrawn[metricName]++
}

func (f *fakeRegistry) advertiseCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.advertised[name]
}

func (f *fakeRegistry) withdrawCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.withdrawn[name]
}

func (f *fakeRegistry) attemptCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts[name]
}

func newHPA(namespace, name string, metrics ...string) *autoscalingv2.HorizontalPodAutoscaler {
	specs := make([]autoscalingv2.MetricSpec, 0, len(metrics))
	for _, m := range metrics {
		specs = append(specs, podsMetric(m))
	}
	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec:       autoscalingv2.HorizontalPodAutoscalerSpec{Metrics: specs},
	}
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	assert.Eventually(t, cond, 2*time.Second, 10*time.Millisecond)
}

func TestWatcher_AdvertisesExistingHPAsOnStart(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		newHPA("ns1", "hpa1", "prometheus.proxy_open_connections.value"),
	)
	reg := newFakeRegistry()
	w := NewWatcher(clientset, reg, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, w.Start(ctx))

	eventually(t, func() bool {
		return reg.advertiseCount("prometheus.proxy_open_connections.value") == 1
	})
}

func TestWatcher_AdvertisesOnHPACreate(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	reg := newFakeRegistry()
	w := NewWatcher(clientset, reg, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, w.Start(ctx))

	_, err := clientset.AutoscalingV2().HorizontalPodAutoscalers("ns1").
		Create(ctx, newHPA("ns1", "hpa1", "foo"), metav1.CreateOptions{})
	require.NoError(t, err)

	eventually(t, func() bool { return reg.advertiseCount("foo") == 1 })
}

func TestWatcher_RetriesTransientAdvertiseFailure(t *testing.T) {
	clientset := fake.NewSimpleClientset(newHPA("ns1", "hpa1", "foo"))
	reg := newFakeRegistry()
	// Fail the first two attempts: the initial advertise on cache sync and the
	// immediate same-event retry, so the metric stays unresolved across events.
	reg.failTimes = 2
	w := NewWatcher(clientset, reg, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, w.Start(ctx))

	// Both initial attempts failed: the metric is not advertised yet.
	eventually(t, func() bool { return reg.attemptCount("foo") >= 2 })
	assert.Equal(t, 0, reg.advertiseCount("foo"))

	// A subsequent HPA event (here an update) must drive the retry to success.
	hpa := newHPA("ns1", "hpa1", "foo")
	hpa.Annotations = map[string]string{"bump": "1"}
	_, err := clientset.AutoscalingV2().HorizontalPodAutoscalers("ns1").
		Update(ctx, hpa, metav1.UpdateOptions{})
	require.NoError(t, err)

	eventually(t, func() bool { return reg.advertiseCount("foo") == 1 })
}

func TestWatcher_WithdrawsOnHPADelete(t *testing.T) {
	clientset := fake.NewSimpleClientset(newHPA("ns1", "hpa1", "foo"))
	reg := newFakeRegistry()
	w := NewWatcher(clientset, reg, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, w.Start(ctx))

	eventually(t, func() bool { return reg.advertiseCount("foo") == 1 })

	require.NoError(t, clientset.AutoscalingV2().HorizontalPodAutoscalers("ns1").
		Delete(ctx, "hpa1", metav1.DeleteOptions{}))

	eventually(t, func() bool { return reg.withdrawCount("foo") == 1 })
}
