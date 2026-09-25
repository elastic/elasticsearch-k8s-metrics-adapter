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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A metric whose first resolution fails transiently is not dropped — it stays
// in the watcher's unresolved set and is retried on a later HPA event,
// eventually getting advertised.
func TestTransientFailureIsRetried(t *testing.T) {
	ctx := context.Background()
	const metric = "prometheus.test_retry.value"

	mockReset(t)
	mockAddKnown(t, metric)
	// Make the first _field_caps probe fail; the retry must succeed.
	mockControl(t, map[string]any{
		"failNext": map[string]any{"path": "_field_caps", "times": 1, "status": 500},
	})

	createPodsHPA(ctx, t, "default", "retry", metric)

	// The first (failing) probe happens; the metric is not advertised yet.
	eventually(t, 30*time.Second, func() bool { return fieldCapsAttempts(t, metric) >= 1 })

	// Drive a retry via an HPA update (status updates from the HPA controller
	// would also trigger this, but we force it for determinism).
	bumpHPA(ctx, t, "default", "retry")

	eventually(t, 60*time.Second, func() bool { return isAdvertised(ctx, t, metric) })
	assert.GreaterOrEqual(t, fieldCapsAttempts(t, metric), 2,
		"expected a second _field_caps probe (the retry) after the transient failure")
}

// The HPA is applied before the field exists in Elasticsearch, as happens when
// an HPA and the workload it scales are rolled out together and the first
// metric document (which creates the field under dynamic mapping) arrives
// later. The first probe finds nothing; once the field appears, a later HPA
// event must advertise the metric without an adapter restart.
func TestNotFoundIsRetriedWhenFieldAppears(t *testing.T) {
	ctx := context.Background()
	const metric = "prometheus.test_late_field.value"

	mockReset(t)
	createPodsHPA(ctx, t, "default", "late-field", metric)

	// The first probe happens and comes back empty: nothing is advertised.
	eventually(t, 30*time.Second, func() bool { return fieldCapsAttempts(t, metric) >= 1 })
	consistently(t, 3*time.Second, func() bool { return !isAdvertised(ctx, t, metric) })

	// The field appears. Not-found names are re-probed at most once per
	// notFoundRetryInterval (1 min) on an HPA event, so keep bumping the HPA
	// until the retry window has passed and the metric is picked up.
	mockAddKnown(t, metric)
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) && !isAdvertised(ctx, t, metric) {
		bumpHPA(ctx, t, "default", "late-field")
		time.Sleep(5 * time.Second)
	}
	require.True(t, isAdvertised(ctx, t, metric), "metric must be advertised once the field appears in Elasticsearch")
	assert.GreaterOrEqual(t, fieldCapsAttempts(t, metric), 2,
		"expected a second _field_caps probe after the not-found answer")
}
