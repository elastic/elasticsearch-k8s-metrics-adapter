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
	"sync/atomic"
	"testing"

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

// resolverFakeClient is a minimal client.Interface whose ResolveCustomMetric
// outcome is controlled by the test. It counts calls so we can assert how often
// a metric is resolved.
type resolverFakeClient struct {
	name      string
	known     map[string]provider.CustomMetricInfo
	err       error
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

func newResolverFakeClient(name string, known ...string) *resolverFakeClient {
	m := make(map[string]provider.CustomMetricInfo, len(known))
	for _, k := range known {
		m[k] = provider.CustomMetricInfo{Metric: k}
	}
	return &resolverFakeClient{name: name, known: m}
}

func TestRegistry_AdvertiseAndWithdraw(t *testing.T) {
	c := newResolverFakeClient("c1", "foo")
	r := NewRegistry().WithResolverClients([]client.Interface{c})

	found, err := r.Advertise(context.Background(), "foo")
	require.NoError(t, err)
	assert.True(t, found)

	// The metric is now listed and routable to the client that serves it.
	assert.ElementsMatch(t, []provider.CustomMetricInfo{{Metric: "foo"}}, r.ListAllCustomMetrics())
	got, err := r.GetCustomMetricClient(provider.CustomMetricInfo{Metric: "foo"})
	require.NoError(t, err)
	assert.Equal(t, "c1", got.GetConfiguration().Name)

	// Withdrawing removes it from both the listing and the routing table.
	r.Withdraw("foo")
	assert.Empty(t, r.ListAllCustomMetrics())
	_, err = r.GetCustomMetricClient(provider.CustomMetricInfo{Metric: "foo"})
	assert.Error(t, err)

	// Withdrawing an unknown metric is a no-op.
	r.Withdraw("never-advertised")
}

func TestRegistry_AdvertiseNotServed(t *testing.T) {
	c := newResolverFakeClient("c1") // knows nothing
	r := NewRegistry().WithResolverClients([]client.Interface{c})

	found, err := r.Advertise(context.Background(), "missing")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Empty(t, r.ListAllCustomMetrics())
}

func TestRegistry_AdvertiseTransientErrorIsReturned(t *testing.T) {
	c := newResolverFakeClient("c1")
	c.err = errors.New("boom")
	r := NewRegistry().WithResolverClients([]client.Interface{c})

	found, err := r.Advertise(context.Background(), "foo")
	assert.Error(t, err)
	assert.False(t, found)
	assert.Empty(t, r.ListAllCustomMetrics())
}

func TestRegistry_AdvertiseFirstMatchingClientWins(t *testing.T) {
	c1 := newResolverFakeClient("c1")        // doesn't know "foo"
	c2 := newResolverFakeClient("c2", "foo") // serves "foo"
	r := NewRegistry().WithResolverClients([]client.Interface{c1, c2})

	found, err := r.Advertise(context.Background(), "foo")
	require.NoError(t, err)
	require.True(t, found)

	got, err := r.GetCustomMetricClient(provider.CustomMetricInfo{Metric: "foo"})
	require.NoError(t, err)
	assert.Equal(t, "c2", got.GetConfiguration().Name)
	assert.Equal(t, int64(1), atomic.LoadInt64(&c1.callCount))
	assert.Equal(t, int64(1), atomic.LoadInt64(&c2.callCount))
}

func TestRegistry_AdvertiseWithoutResolverClients(t *testing.T) {
	r := NewRegistry()
	found, err := r.Advertise(context.Background(), "foo")
	require.NoError(t, err)
	assert.False(t, found)
}
