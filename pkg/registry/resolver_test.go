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
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/metrics/pkg/apis/custom_metrics"
	"k8s.io/metrics/pkg/apis/external_metrics"
	"sigs.k8s.io/custom-metrics-apiserver/pkg/provider"

	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/client"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/config"
)

// resolverFakeClient is a minimal client.Interface for resolver tests.
// It counts ResolveCustomMetric calls and lets the test control its outcome.
type resolverFakeClient struct {
	name      string
	known     map[string]provider.CustomMetricInfo
	err       error
	delay     time.Duration
	callCount int64
}

func (f *resolverFakeClient) GetConfiguration() config.MetricServer {
	return config.MetricServer{Name: f.name}
}

func (f *resolverFakeClient) ListCustomMetricInfos() (map[provider.CustomMetricInfo]struct{}, error) {
	return nil, nil
}

func (f *resolverFakeClient) ResolveCustomMetric(_ context.Context, metricName string) (provider.CustomMetricInfo, bool, error) {
	atomic.AddInt64(&f.callCount, 1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.err != nil {
		return provider.CustomMetricInfo{}, false, f.err
	}
	info, ok := f.known[metricName]
	return info, ok, nil
}

func (f *resolverFakeClient) GetMetricByName(types.NamespacedName, provider.CustomMetricInfo, labels.Selector) (*custom_metrics.MetricValue, error) {
	panic("not implemented")
}

func (f *resolverFakeClient) GetMetricBySelector(string, labels.Selector, provider.CustomMetricInfo, labels.Selector) (*custom_metrics.MetricValueList, error) {
	panic("not implemented")
}

func (f *resolverFakeClient) ListExternalMetrics() (map[provider.ExternalMetricInfo]struct{}, error) {
	return nil, nil
}

func (f *resolverFakeClient) GetExternalMetric(string, string, labels.Selector) (*external_metrics.ExternalMetricValueList, error) {
	panic("not implemented")
}

var _ client.Interface = &resolverFakeClient{}

func newFakeClient(name string, known ...string) *resolverFakeClient {
	m := make(map[string]provider.CustomMetricInfo, len(known))
	for _, k := range known {
		m[k] = provider.CustomMetricInfo{Metric: k}
	}
	return &resolverFakeClient{name: name, known: m}
}

func TestResolver_HitPositiveCache(t *testing.T) {
	c := newFakeClient("c1", "foo")
	r := NewResolver([]client.Interface{c}, 0)

	for i := 0; i < 3; i++ {
		entry, err := r.Resolve(context.Background(), "foo")
		require.NoError(t, err)
		require.NotNil(t, entry)
		assert.Equal(t, "foo", entry.Info.Metric)
	}
	assert.Equal(t, int64(1), atomic.LoadInt64(&c.callCount), "only one upstream call expected after positive cache hit")
}

func TestResolver_NegativeCache(t *testing.T) {
	c := newFakeClient("c1") // empty: nothing known
	r := NewResolver([]client.Interface{c}, time.Hour)

	for i := 0; i < 3; i++ {
		entry, err := r.Resolve(context.Background(), "missing")
		require.NoError(t, err)
		assert.Nil(t, entry)
	}
	assert.Equal(t, int64(1), atomic.LoadInt64(&c.callCount), "only one upstream call expected after negative cache hit")
}

func TestResolver_NegativeCacheExpires(t *testing.T) {
	c := newFakeClient("c1")
	r := NewResolver([]client.Interface{c}, time.Hour)

	now := time.Unix(1_000_000, 0)
	r.nowFn = func() time.Time { return now }

	_, err := r.Resolve(context.Background(), "missing")
	require.NoError(t, err)
	assert.Equal(t, int64(1), atomic.LoadInt64(&c.callCount))

	// Still cached.
	_, err = r.Resolve(context.Background(), "missing")
	require.NoError(t, err)
	assert.Equal(t, int64(1), atomic.LoadInt64(&c.callCount))

	// Advance past TTL — should re-query.
	now = now.Add(time.Hour + time.Second)
	_, err = r.Resolve(context.Background(), "missing")
	require.NoError(t, err)
	assert.Equal(t, int64(2), atomic.LoadInt64(&c.callCount))
}

func TestResolver_TransientErrorNotCached(t *testing.T) {
	c := newFakeClient("c1")
	c.err = errors.New("boom")
	r := NewResolver([]client.Interface{c}, time.Hour)

	for i := 0; i < 3; i++ {
		_, err := r.Resolve(context.Background(), "foo")
		assert.Error(t, err)
	}
	assert.Equal(t, int64(3), atomic.LoadInt64(&c.callCount), "transient errors must not be cached")
}

func TestResolver_FirstMatchingClientWins(t *testing.T) {
	c1 := newFakeClient("c1") // doesn't know "foo"
	c2 := newFakeClient("c2", "foo")
	r := NewResolver([]client.Interface{c1, c2}, 0)

	entry, err := r.Resolve(context.Background(), "foo")
	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.Equal(t, "c2", entry.Client.GetConfiguration().Name)
}

func TestResolver_Singleflight(t *testing.T) {
	c := newFakeClient("c1", "foo")
	c.delay = 50 * time.Millisecond
	r := NewResolver([]client.Interface{c}, 0)

	const N = 20
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			entry, err := r.Resolve(context.Background(), "foo")
			assert.NoError(t, err)
			assert.NotNil(t, entry)
		}()
	}
	wg.Wait()
	// Singleflight collapses concurrent in-flight calls into one upstream call.
	assert.Equal(t, int64(1), atomic.LoadInt64(&c.callCount))
}
