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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/custom-metrics-apiserver/pkg/provider"

	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/client"
)

// customMetricInfo builds a CustomMetricInfo with the same field shape the
// production Elasticsearch client populates (pods resource, namespaced), so the
// registry key used in these tests matches the real routing key.
func customMetricInfo(name string) provider.CustomMetricInfo {
	return provider.CustomMetricInfo{
		GroupResource: schema.GroupResource{Resource: "pods"},
		Namespaced:    true,
		Metric:        name,
	}
}

// newResolverFakeClient returns a fake client that serves the given metric
// names, keyed with the production CustomMetricInfo shape.
func newResolverFakeClient(name string, known ...string) *fakeMetricsClient {
	c := newFakeMetricsClient(name, 0)
	for _, k := range known {
		c.customMetrics[customMetricInfo(k)] = struct{}{}
	}
	return c
}

func TestRegistry_AdvertiseAndWithdraw(t *testing.T) {
	c := newResolverFakeClient("c1", "foo")
	r := NewRegistry().WithResolverClients([]client.Interface{c})

	found, err := r.Advertise(context.Background(), "foo")
	require.NoError(t, err)
	assert.True(t, found)

	// The metric is now listed and routable to the client that serves it.
	assert.ElementsMatch(t, []provider.CustomMetricInfo{customMetricInfo("foo")}, r.ListAllCustomMetrics())
	got, err := r.GetCustomMetricClient(customMetricInfo("foo"))
	require.NoError(t, err)
	assert.Equal(t, "c1", got.GetConfiguration().Name)

	// Withdrawing removes it from both the listing and the routing table.
	r.Withdraw("foo")
	assert.Empty(t, r.ListAllCustomMetrics())
	_, err = r.GetCustomMetricClient(customMetricInfo("foo"))
	assert.Error(t, err)

	// Withdrawing an unknown metric is a no-op.
	r.Withdraw("never-advertised")
}

func TestRegistry_WithdrawKeepsOtherBackends(t *testing.T) {
	info := customMetricInfo("foo")

	// A periodically-discovered backend (e.g. custom_api) already serves foo.
	periodic := newResolverFakeClient("periodic", "foo")
	r := NewRegistry()
	r.UpdateCustomMetrics(periodic, map[provider.CustomMetricInfo]struct{}{info: {}})

	// An ES resolver client advertises the same metric because an HPA references it.
	resolver := newResolverFakeClient("resolver", "foo")
	r.WithResolverClients([]client.Interface{resolver})
	found, err := r.Advertise(context.Background(), "foo")
	require.NoError(t, err)
	require.True(t, found)

	// Withdrawing (last HPA reference gone) must not evict the periodic backend.
	r.Withdraw("foo")
	assert.ElementsMatch(t, []provider.CustomMetricInfo{info}, r.ListAllCustomMetrics())
	got, err := r.GetCustomMetricClient(info)
	require.NoError(t, err)
	assert.Equal(t, "periodic", got.GetConfiguration().Name)
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
	c.resolveErr = errors.New("boom")
	r := NewRegistry().WithResolverClients([]client.Interface{c})

	found, err := r.Advertise(context.Background(), "foo")
	assert.Error(t, err)
	assert.False(t, found)
	assert.Empty(t, r.ListAllCustomMetrics())
}

func TestRegistry_AdvertiseTriesLaterClientAfterError(t *testing.T) {
	c1 := newResolverFakeClient("c1") // transiently failing
	c1.resolveErr = errors.New("boom")
	c2 := newResolverFakeClient("c2", "foo") // serves "foo"
	r := NewRegistry().WithResolverClients([]client.Interface{c1, c2})

	// c1's error must not prevent c2 from resolving the metric.
	found, err := r.Advertise(context.Background(), "foo")
	require.NoError(t, err)
	require.True(t, found)

	got, err := r.GetCustomMetricClient(customMetricInfo("foo"))
	require.NoError(t, err)
	assert.Equal(t, "c2", got.GetConfiguration().Name)
	assert.Equal(t, int64(1), c1.resolveCalls.Load())
	assert.Equal(t, int64(1), c2.resolveCalls.Load())
}

func TestRegistry_AdvertiseFirstMatchingClientWins(t *testing.T) {
	c1 := newResolverFakeClient("c1")        // doesn't know "foo"
	c2 := newResolverFakeClient("c2", "foo") // serves "foo"
	r := NewRegistry().WithResolverClients([]client.Interface{c1, c2})

	found, err := r.Advertise(context.Background(), "foo")
	require.NoError(t, err)
	require.True(t, found)

	got, err := r.GetCustomMetricClient(customMetricInfo("foo"))
	require.NoError(t, err)
	assert.Equal(t, "c2", got.GetConfiguration().Name)
	assert.Equal(t, int64(1), c1.resolveCalls.Load())
	assert.Equal(t, int64(1), c2.resolveCalls.Load())
}

func TestRegistry_AdvertiseWithoutResolverClients(t *testing.T) {
	r := NewRegistry()
	found, err := r.Advertise(context.Background(), "foo")
	require.NoError(t, err)
	assert.False(t, found)
}
