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
)

// Scenario 8 (Issue A): a metric whose first resolution fails transiently is not
// dropped — it stays in the watcher's unresolved set and is retried on a later
// HPA event, eventually getting advertised.
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
