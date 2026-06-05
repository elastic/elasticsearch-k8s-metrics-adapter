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

package elasticsearch

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	esv8 "github.com/elastic/go-elasticsearch/v9"

	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/config"
)

// fieldCapsResponse returns a minimal _field_caps JSON body for the fields
// that the old mapping.json test expected to be discovered.
const fieldCapsResponse = `{
  "fields": {
    "event.duration":                  {"long":   {"type":"long",   "metadata_field":false,"searchable":true,"aggregatable":true}},
    "host.cpu.usage":                  {"scaled_float":{"type":"scaled_float","metadata_field":false,"searchable":true,"aggregatable":true}},
    "metricset.period":                {"long":   {"type":"long",   "metadata_field":false,"searchable":true,"aggregatable":true}},
    "root_metric":                     {"long":   {"type":"long",   "metadata_field":false,"searchable":true,"aggregatable":true}},
    "system.cpu.cores":                {"long":   {"type":"long",   "metadata_field":false,"searchable":true,"aggregatable":true}},
    "system.cpu.idle.norm.pct":        {"scaled_float":{"type":"scaled_float","metadata_field":false,"searchable":true,"aggregatable":true}},
    "system.cpu.idle.pct":             {"scaled_float":{"type":"scaled_float","metadata_field":false,"searchable":true,"aggregatable":true}},
    "system.cpu.iowait.norm.pct":      {"scaled_float":{"type":"scaled_float","metadata_field":false,"searchable":true,"aggregatable":true}},
    "system.cpu.iowait.pct":           {"scaled_float":{"type":"scaled_float","metadata_field":false,"searchable":true,"aggregatable":true}},
    "system.cpu.irq.norm.pct":         {"scaled_float":{"type":"scaled_float","metadata_field":false,"searchable":true,"aggregatable":true}},
    "system.cpu.irq.pct":              {"scaled_float":{"type":"scaled_float","metadata_field":false,"searchable":true,"aggregatable":true}},
    "system.cpu.nice.norm.pct":        {"scaled_float":{"type":"scaled_float","metadata_field":false,"searchable":true,"aggregatable":true}},
    "system.cpu.nice.pct":             {"scaled_float":{"type":"scaled_float","metadata_field":false,"searchable":true,"aggregatable":true}},
    "system.cpu.softirq.norm.pct":     {"scaled_float":{"type":"scaled_float","metadata_field":false,"searchable":true,"aggregatable":true}},
    "system.cpu.softirq.pct":          {"scaled_float":{"type":"scaled_float","metadata_field":false,"searchable":true,"aggregatable":true}},
    "system.cpu.steal.norm.pct":       {"scaled_float":{"type":"scaled_float","metadata_field":false,"searchable":true,"aggregatable":true}},
    "system.cpu.steal.pct":            {"scaled_float":{"type":"scaled_float","metadata_field":false,"searchable":true,"aggregatable":true}},
    "system.cpu.system.norm.pct":      {"scaled_float":{"type":"scaled_float","metadata_field":false,"searchable":true,"aggregatable":true}},
    "system.cpu.system.pct":           {"scaled_float":{"type":"scaled_float","metadata_field":false,"searchable":true,"aggregatable":true}},
    "system.cpu.total.norm.pct":       {"scaled_float":{"type":"scaled_float","metadata_field":false,"searchable":true,"aggregatable":true}},
    "system.cpu.total.pct":            {"scaled_float":{"type":"scaled_float","metadata_field":false,"searchable":true,"aggregatable":true}},
    "system.cpu.user.norm.pct":        {"scaled_float":{"type":"scaled_float","metadata_field":false,"searchable":true,"aggregatable":true}},
    "system.cpu.user.pct":             {"scaled_float":{"type":"scaled_float","metadata_field":false,"searchable":true,"aggregatable":true}},
    "some.keyword.field":              {"keyword":{"type":"keyword","metadata_field":false,"searchable":true,"aggregatable":true}}
  }
}`

func Test_discoverFieldCaps(t *testing.T) {
	// Spin up a fake ES that returns fieldCapsResponse for any request.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		_, _ = w.Write([]byte(fieldCapsResponse))
	}))
	defer srv.Close()

	esClient, err := esv8.NewClient(esv8.Config{Addresses: []string{srv.URL}}) //nolint:staticcheck
	require.NoError(t, err)

	testConfig, err := config.From([]byte(`
metricServers:
  - name: k8s-region-observability-cluster
    serverType: elasticsearch
    metricSets:
      - indices: [ '*' ]
`))
	require.NoError(t, err)

	noopNamer, err := config.NewNamer(nil)
	require.NoError(t, err)
	rec := newRecorder(noopNamer)

	metricSet := testConfig.MetricServers[0].MetricSets[0]
	require.NoError(t, discoverFieldCaps(logr.Discard(), metricSet, esClient, rec))

	got := make([]string, 0, len(rec.metrics))
	for metric := range rec.metrics {
		got = append(got, metric)
	}
	sort.Strings(got)

	want := []string{
		"event.duration",
		"host.cpu.usage",
		"metricset.period",
		"root_metric",
		"system.cpu.cores",
		"system.cpu.idle.norm.pct",
		"system.cpu.idle.pct",
		"system.cpu.iowait.norm.pct",
		"system.cpu.iowait.pct",
		"system.cpu.irq.norm.pct",
		"system.cpu.irq.pct",
		"system.cpu.nice.norm.pct",
		"system.cpu.nice.pct",
		"system.cpu.softirq.norm.pct",
		"system.cpu.softirq.pct",
		"system.cpu.steal.norm.pct",
		"system.cpu.steal.pct",
		"system.cpu.system.norm.pct",
		"system.cpu.system.pct",
		"system.cpu.total.norm.pct",
		"system.cpu.total.pct",
		"system.cpu.user.norm.pct",
		"system.cpu.user.pct",
		// "some.keyword.field" is absent: keyword is not a numeric type
	}
	assert.Empty(t, cmp.Diff(want, got))
}
