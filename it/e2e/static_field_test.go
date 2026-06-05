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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// staticMetric is the search-based field configured in it/testdata/values-e2e.yaml.
const staticMetric = "prometheus.static_test.value"

// Scenario 7 (Issue B): a static, search-based field is advertised and served in
// hpa mode. Such a field is pre-registered at client construction
// (recordStaticFields), so it must resolve via the fast path — never a
// _field_caps probe — and its value comes from its configured _search body.
func TestStaticSearchFieldServed(t *testing.T) {
	ctx := context.Background()

	mockReset(t)
	// Needed so the mock returns a hit for the static field's _search.
	mockAddKnown(t, staticMetric)

	createPodsHPA(ctx, t, "default", "static-field", staticMetric)
	eventually(t, 30*time.Second, func() bool { return isAdvertised(ctx, t, staticMetric) })

	// Issue B: the static field is resolved without any _field_caps probe.
	for _, r := range mockRequests(t) {
		if strings.HasSuffix(r.Path, "/_field_caps") {
			assert.NotContains(t, r.Query, staticMetric,
				"a static field must be resolved via the fast path, not _field_caps")
		}
	}

	// Its value is served via the configured search body.
	values, code := getPodMetric(ctx, t, metricQueryNamespace, staticMetric)
	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, values.Items, "expected a value for the static field")
	assert.Equal(t, staticMetric, values.Items[0].Metric.Name)
	assert.NotEmpty(t, values.Items[0].Value)
}
