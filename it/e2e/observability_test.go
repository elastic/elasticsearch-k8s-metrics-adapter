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

// Scenario 3: discovery in hpa mode uses _field_caps and never _mapping.
// Proven from the mock's request log.
func TestNoMappingCalls(t *testing.T) {
	ctx := context.Background()
	const metric = "prometheus.test_no_mapping.value"

	mockReset(t)
	mockAddKnown(t, metric)
	createPodsHPA(ctx, t, "default", "no-mapping", metric)
	eventually(t, 30*time.Second, func() bool { return isAdvertised(ctx, t, metric) })

	reqs := mockRequests(t)
	var sawFieldCaps bool
	for _, r := range reqs {
		assert.NotContains(t, r.Path, "_mapping", "hpa mode must never call _mapping (got %s)", r.Path)
		if strings.Contains(r.Path, "_field_caps") {
			sawFieldCaps = true
		}
	}
	assert.True(t, sawFieldCaps, "expected at least one _field_caps call during resolution")
}

// Scenario 9: fetching a value issues a _search filtered by the field's
// existence and the target pod's namespace and name.
func TestValueQueryShape(t *testing.T) {
	ctx := context.Background()

	mockReset(t)
	_, code := getPodMetric(ctx, t, metricQueryNamespace, startupMetric)
	require.Equal(t, http.StatusOK, code)

	var searchBody string
	for _, r := range mockRequests(t) {
		if strings.HasSuffix(r.Path, "/_search") && strings.Contains(r.Body, startupMetric) {
			searchBody = r.Body
			break
		}
	}
	require.NotEmpty(t, searchBody, "expected a _search request referencing %s", startupMetric)
	assert.Contains(t, searchBody, startupMetric, "search should filter on the metric field")
	assert.Contains(t, searchBody, "kubernetes.namespace", "search should filter by namespace")
	assert.Contains(t, searchBody, "kubernetes.pod.name", "search should filter by pod")
}
