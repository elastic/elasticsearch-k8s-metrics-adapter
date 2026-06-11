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

//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Scenario 10: the adapter comes up Ready and its aggregated API is available.
// (TestMain already gates on this; this test asserts it explicitly and proves
// discovery returns a 200, not a 503.)
func TestAdapterReadyAndAggregated(t *testing.T) {
	ctx := context.Background()
	_, code, err := rawGet(ctx, customMetricsBase)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, code, "custom.metrics.k8s.io/v1beta2 discovery should be served")
}

// Scenario 1: a metric referenced by an HPA that existed BEFORE the adapter
// started must be advertised after the watcher's initial cache sync, and its
// value must be fetchable. The HPA is applied by `make e2e-up`
// (it/testdata/hpa-startup.yaml) ahead of the adapter.
func TestAdvertiseExistingHPAOnStartup(t *testing.T) {
	ctx := context.Background()

	eventually(t, 60*time.Second, func() bool {
		return isAdvertised(ctx, t, startupMetric)
	})

	values, code := getPodMetric(ctx, t, metricQueryNamespace, startupMetric)
	require.Equal(t, http.StatusOK, code, "fetching the advertised metric value should succeed")
	require.NotEmpty(t, values.Items, "expected at least one pod metric value from %s", metricQueryNamespace)
	assert.Equal(t, startupMetric, values.Items[0].Metric.Name)
	assert.NotEmpty(t, values.Items[0].Value, "metric value should be populated")
}

// Scenario 2: a metric referenced by an HPA created while the adapter is
// running becomes advertised shortly after.
func TestAdvertiseOnHPACreate(t *testing.T) {
	ctx := context.Background()
	const metric = "prometheus.test_advertise_on_create.value"

	// Make the field resolvable before the HPA exists, so the watcher's resolve
	// on the create event succeeds.
	mockAddKnown(t, metric)
	assert.False(t, isAdvertised(ctx, t, metric), "metric must not be advertised before any HPA references it")

	createPodsHPA(ctx, t, "default", "advertise-on-create", metric)

	eventually(t, 30*time.Second, func() bool {
		return isAdvertised(ctx, t, metric)
	})
}

// Scenario 4: a metric shared by two HPAs is withdrawn only when the LAST
// referencing HPA is deleted.
func TestWithdrawOnLastHPADelete(t *testing.T) {
	ctx := context.Background()
	const metric = "prometheus.test_withdraw.value"
	mockAddKnown(t, metric)

	createPodsHPA(ctx, t, "default", "withdraw-a", metric)
	createPodsHPA(ctx, t, "default", "withdraw-b", metric)
	eventually(t, 30*time.Second, func() bool { return isAdvertised(ctx, t, metric) })

	// Delete the first HPA: still referenced by withdraw-b, so it must stay.
	deleteHPA(ctx, t, "default", "withdraw-a")
	consistently(t, 5*time.Second, func() bool { return isAdvertised(ctx, t, metric) })

	// Delete the last HPA: now it must be withdrawn.
	deleteHPA(ctx, t, "default", "withdraw-b")
	eventually(t, 30*time.Second, func() bool { return !isAdvertised(ctx, t, metric) })
}

// Scenario 5: a metric no HPA references is not advertised, and querying its
// value returns 404 from the adapter.
func TestUnadvertisedMetricNotFound(t *testing.T) {
	ctx := context.Background()
	const metric = "prometheus.never_referenced.value"

	require.False(t, isAdvertised(ctx, t, metric))
	_, code := getPodMetric(ctx, t, metricQueryNamespace, metric)
	assert.Equal(t, http.StatusNotFound, code, "unadvertised metric should 404")
}
