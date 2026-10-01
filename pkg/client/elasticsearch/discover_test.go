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
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"sigs.k8s.io/custom-metrics-apiserver/pkg/provider"

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
    "some.keyword.field":              {"keyword":{"type":"keyword","metadata_field":false,"searchable":true,"aggregatable":true}},
    "_seq_no":                         {"long":   {"type":"long",   "metadata_field":true, "searchable":true,"aggregatable":true}},
    "_doc_count":                      {"long":   {"type":"long",   "metadata_field":true, "searchable":false,"aggregatable":false}},
    "_index":                          {"_index": {"type":"_index", "metadata_field":true, "searchable":true,"aggregatable":true}}
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
	// The mock ignores types= and returns a keyword field; the client-side
	// filter must exclude it.
	mc := &MetricsClient{logger: logr.Discard(), Client: esClient}
	require.NoError(t, mc.discoverFieldCaps(context.Background(), metricSet, rec))

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
		// "some.keyword.field" is absent: keyword is not a numeric type.
		// "_seq_no", "_doc_count" and "_index" are absent: metadata fields are
		// never metrics, even when their type is numeric.
	}
	assert.Empty(t, cmp.Diff(want, got))
}

// A cluster that rejects the types= parameter (ES < 8.2) switches the client to
// unfiltered requests for its lifetime.
func TestFieldCaps_FallsBackWhenTypesParamUnsupported(t *testing.T) {
	var withTypes, withoutTypes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if r.URL.Query().Has("types") {
			withTypes++
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"root_cause":[{"type":"illegal_argument_exception","reason":"request [/metrics-*/_field_caps] contains unrecognized parameter: [types]"}],"type":"illegal_argument_exception","reason":"request [/metrics-*/_field_caps] contains unrecognized parameter: [types]"},"status":400}`))
			return
		}
		withoutTypes++
		_, _ = w.Write([]byte(`{"fields":{"foo":{"long":{"type":"long","metadata_field":false}}}}`))
	}))
	defer srv.Close()
	esClient, err := esv8.NewClient(esv8.Config{Addresses: []string{srv.URL}}) //nolint:staticcheck
	require.NoError(t, err)
	mc := &MetricsClient{logger: logr.Discard(), Client: esClient}

	fields, err := mc.fieldCaps(context.Background(), []string{"metrics-*"}, []string{"foo"})
	require.NoError(t, err)
	assert.Contains(t, fields, "foo")
	assert.Equal(t, 1, withTypes, "types= is tried once")
	assert.Equal(t, 1, withoutTypes, "then the request is retried without it")
	assert.True(t, mc.typesFilterUnsupported.Load())

	_, err = mc.fieldCaps(context.Background(), []string{"metrics-*"}, []string{"foo"})
	require.NoError(t, err)
	assert.Equal(t, 1, withTypes, "the rejection is remembered")
	assert.Equal(t, 2, withoutTypes)
}

// Any other 400 is a real error and does not disable the types= filter.
func TestFieldCaps_OtherBadRequestIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"illegal_argument_exception","reason":"bad request"},"status":400}`))
	}))
	defer srv.Close()
	esClient, err := esv8.NewClient(esv8.Config{Addresses: []string{srv.URL}}) //nolint:staticcheck
	require.NoError(t, err)
	mc := &MetricsClient{logger: logr.Discard(), Client: esClient}

	_, err = mc.fieldCaps(context.Background(), []string{"metrics-*"}, []string{"foo"})
	require.Error(t, err)
	assert.False(t, errors.Is(err, errTypesParamUnsupported))
	assert.False(t, mc.typesFilterUnsupported.Load())
}

// A metric set whose index pattern errors must not shadow a later metric set
// that serves the metric.
func TestResolveCustomMetric_TriesLaterMetricSetAfterError(t *testing.T) {
	const metric = "prometheus.foo.value"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		switch {
		case r.URL.Path == "/":
			_, _ = w.Write([]byte(`{"version":{"number":"9.4.2"}}`))
		case strings.HasPrefix(r.URL.Path, "/bad-"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
		default:
			_, _ = w.Write([]byte(`{"fields":{"` + metric + `":{"long":{"type":"long","metadata_field":false}}}}`))
		}
	}))
	defer srv.Close()
	esClient, err := esv8.NewClient(esv8.Config{Addresses: []string{srv.URL}}) //nolint:staticcheck
	require.NoError(t, err)

	cfg, err := config.From([]byte(`
metricServers:
  - name: es
    serverType: elasticsearch
    metricSets:
      - indices: [ 'bad-*' ]
      - indices: [ 'good-*' ]
`))
	require.NoError(t, err)
	namer, err := config.NewNamer(nil)
	require.NoError(t, err)
	mc := &MetricsClient{
		logger:          logr.Discard(),
		Client:          esClient,
		metricServerCfg: cfg.MetricServers[0],
		metrics:         map[string]provider.CustomMetricInfo{},
		indexedMetrics:  map[string]MetricMetadata{},
		namer:           namer,
	}

	info, found, err := mc.ResolveCustomMetric(context.Background(), metric)
	require.NoError(t, err)
	require.True(t, found, "the second metric set serves the metric")
	assert.Equal(t, metric, info.Metric)
	assert.Equal(t, []string{"good-*"}, mc.indexedMetrics[metric].Indices)

	// When no metric set serves it, the error from the failing one is surfaced
	// so the caller retries instead of treating the metric as absent.
	_, found, err = mc.ResolveCustomMetric(context.Background(), "prometheus.other.value")
	assert.False(t, found)
	require.Error(t, err)
}

func Test_recordStaticFields_registersAliasWithNamer(t *testing.T) {
	cfg, err := config.From([]byte(`
metricServers:
  - name: es
    serverType: elasticsearch
    rename:
      matches: "^(.*)$"
      as: "${1}@es"
    metricSets:
      - indices: [ 'metrics-*' ]
        fields:
          - name: my_static_field
            search:
              metricPath: ".value"
              timestampPath: ".timestamp"
              body: "{}"
`))
	require.NoError(t, err)

	server := cfg.MetricServers[0]
	namer, err := config.NewNamer(server.Rename)
	require.NoError(t, err)
	rec := newRecorder(namer)

	require.NoError(t, recordStaticFields(server, rec))

	// The exposed metric name is the alias, and the namer resolves it back to the
	// field name; the metadata map is keyed by the field name. Without namer
	// registration, the query-time namer.Get(alias) would fail.
	info, ok := rec.metrics["my_static_field"]
	require.True(t, ok)
	assert.Equal(t, "my_static_field@es", info.Metric)

	original, ok := namer.Get(info.Metric)
	require.True(t, ok)
	assert.Equal(t, "my_static_field", original)

	_, ok = rec.indexedMetrics["my_static_field"]
	assert.True(t, ok)
}
