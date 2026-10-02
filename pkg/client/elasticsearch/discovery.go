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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/itchyny/gojq"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/custom-metrics-apiserver/pkg/provider"

	esv8 "github.com/elastic/go-elasticsearch/v9"
	"github.com/elastic/go-elasticsearch/v9/esapi"

	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/config"
)

// numericTypes is the single source of truth for the Elasticsearch numeric
// field types the adapter is willing to expose. It is used as the `types=`
// filter on _field_caps requests so non-numeric fields are excluded server-side.
var numericTypes = []string{
	"byte", "double", "float", "half_float",
	"integer", "long", "scaled_float", "short", "unsigned_long",
}

var numericTypesSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(numericTypes))
	for _, t := range numericTypes {
		m[t] = struct{}{}
	}
	return m
}()

// fieldTypes is the per-field slice of a _field_caps response: a map from ES
// type name to that type's capabilities. Only the type name and the metadata
// flag matter to us, so the value struct carries just those.
type fieldTypes = map[string]struct {
	Type          string `json:"type"`
	MetadataField bool   `json:"metadata_field"`
}

// fieldCaps maps a field name to its fieldTypes. It is the shape decoded from
// the "fields" object of a _field_caps response.
type fieldCaps = map[string]fieldTypes

// Bounds for a single _field_caps request. The HTTP client has no timeout
// unless clientConfig.timeout is set. A single-field lookup (hpa mode) is
// cheap, so it gets a short bound. A fields=* discovery over a large index
// pattern (full mode) can take much longer, so it gets a generous one: before
// _field_caps replaced _mapping it had no bound at all, and a bound a large
// cluster never meets would fail every discovery cycle.
const (
	fieldCapsLookupTimeout    = 10 * time.Second
	fieldCapsDiscoveryTimeout = 60 * time.Second
)

// errTypesParamUnsupported is returned by fetchFieldCaps when Elasticsearch
// rejects the `types=` parameter. It was added in 8.2
// (elastic/elasticsearch#83636); older clusters answer HTTP 400 with
// "contains unrecognized parameter: [types]".
var errTypesParamUnsupported = errors.New("_field_caps types= parameter not supported")

// fieldCaps runs a _field_caps request filtered server-side to numericTypes.
// The first time the cluster rejects the types= parameter, the client switches
// to unfiltered requests for its lifetime and callers filter the (larger)
// response via hasNumericType, so pre-8.2 clusters keep working. There is no
// version probe: the rejection itself is the detection, and any other error is
// returned as usual. timeout bounds each request.
func (mc *MetricsClient) fieldCaps(ctx context.Context, indices, fields []string, timeout time.Duration) (fieldCaps, error) {
	if !mc.typesFilterUnsupported.Load() {
		caps, err := fetchFieldCaps(ctx, mc.Client, indices, fields, numericTypes, timeout)
		if !errors.Is(err, errTypesParamUnsupported) {
			return caps, err
		}
		mc.logger.Info("Elasticsearch rejected the _field_caps types= parameter (added in 8.2); filtering field types client-side from now on")
		mc.typesFilterUnsupported.Store(true)
	}
	return fetchFieldCaps(ctx, mc.Client, indices, fields, nil, timeout)
}

// fetchFieldCaps runs a _field_caps request for the given fields against the
// index pattern and returns the decoded "fields" map. When types is non-empty
// it is sent as the server-side type filter; a cluster that does not know the
// parameter answers HTTP 400, reported as errTypesParamUnsupported so the
// caller can retry without it. Without the filter the response carries all
// field types and callers must filter client-side via hasNumericType.
//
// The request tolerates missing or empty index patterns
// (AllowNoIndices/IgnoreUnavailable) and uses filter_path=fields to drop the
// top-level "indices" array from the response. For an index pattern like
// metrics-* that matches thousands of data-stream backing indices, that array
// dominates the payload even though we only care about field types.
func fetchFieldCaps(ctx context.Context, esClient *esv8.Client, indices, fields, types []string, timeout time.Duration) (fieldCaps, error) {
	// Bound each request on its own, so a hung index pattern or cluster cannot
	// consume the time budget of the next metric set or resolver client.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req := esapi.FieldCapsRequest{
		Index:             indices,
		Fields:            fields,
		AllowNoIndices:    ptr.To(true),
		IgnoreUnavailable: ptr.To(true),
		FilterPath:        []string{"fields"},
	}
	if len(types) > 0 {
		req.Types = types
	}
	res, err := req.Do(ctx, esClient)
	if err != nil {
		return nil, fmt.Errorf("_field_caps request failed: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		if res.StatusCode == http.StatusBadRequest && len(types) > 0 &&
			strings.Contains(string(body), "unrecognized parameter") && strings.Contains(string(body), "[types]") {
			return nil, fmt.Errorf("[%s] _field_caps for %v: %w", res.Status(), indices, errTypesParamUnsupported)
		}
		return nil, fmt.Errorf("[%s] _field_caps error for %v", res.Status(), indices)
	}
	var r struct {
		Fields fieldCaps `json:"fields"`
	}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("error parsing _field_caps response: %w", err)
	}
	return r.Fields, nil
}

// hasNumericType reports whether the given per-field type set contains at least
// one type the adapter is willing to expose. _field_caps is already filtered
// server-side via Types, but we re-check client-side so correctness does not
// depend on that filter being honored.
//
// Metadata fields are excluded. _field_caps?fields=* lists them alongside
// mapped fields, and some are numeric (_seq_no and _doc_count are long), but
// they are not metrics. The former _mapping walk only descended "properties"
// and never saw them.
//
// _field_caps also lists alias, runtime and numeric multi-fields, which that
// walk skipped too. They cannot be told apart in the response, so they are
// kept; see "Fields listed by _field_caps" in docs/hpa-discovery.md.
func hasNumericType(types fieldTypes) bool {
	for t, caps := range types {
		if caps.MetadataField {
			continue
		}
		if _, ok := numericTypesSet[t]; ok {
			return true
		}
	}
	return false
}

type MetricMetadata struct {
	Fields          config.Fields
	Search          *config.Search
	Indices         []string
	MetricsProvider provider.MetricsProvider
}

// discoverMetrics attempts to create a list of the available metrics and maintains an internal state.
func (mc *MetricsClient) discoverMetrics() error {
	namer, err := config.NewNamer(mc.GetConfiguration().Rename)
	if err != nil {
		return fmt.Errorf("%s: failed to create namer: %v", mc.GetConfiguration().Name, err)
	}
	metricRecorder := newRecorder(namer)

	// We first record static fields, they do not require to read the mapping.
	if err := recordStaticFields(mc.metricServerCfg, metricRecorder); err != nil {
		return err
	}

	for _, metricSet := range mc.metricServerCfg.MetricSets {
		if err := mc.discoverFieldCaps(context.Background(), metricSet, metricRecorder); err != nil {
			return err
		}
	}

	mc.lock.Lock()
	defer mc.lock.Unlock()
	mc.metrics = metricRecorder.metrics
	mc.indexedMetrics = metricRecorder.indexedMetrics
	mc.namer = namer
	return nil
}

// discoverFieldCaps calls _field_caps for all numeric field types in the
// configured index pattern and registers every matching field with the recorder.
//
// It replaces the former getMappingFor / _processMappingDocument approach which
// fetched the full nested _mapping response (~43 MB for metrics-*) and walked
// it recursively. _field_caps returns a flat structure, is filtered server-side
// to numeric types, and is ~5x smaller on the wire (see fetchFieldCaps). It can
// list a few more fields than the walk did (see hasNumericType).
func (mc *MetricsClient) discoverFieldCaps(ctx context.Context, metricSet config.MetricSet, recorder *recorder) error {
	fields, err := mc.fieldCaps(ctx, metricSet.Indices, []string{"*"}, fieldCapsDiscoveryTimeout)
	if err != nil {
		return err
	}
	if len(fields) == 0 {
		mc.logger.Info("No numeric fields found", "index_pattern", strings.Join(metricSet.Indices, ","))
		return nil
	}

	mc.logger.V(1).Info("Discovered fields via _field_caps",
		"count", len(fields),
		"index_pattern", strings.Join(metricSet.Indices, ","))

	for fieldName, typesMap := range fields {
		if !hasNumericType(typesMap) {
			continue
		}

		fieldMeta := metricSet.Fields.FindMetadata(fieldName)
		if fieldMeta == nil {
			// Field does not match any configured pattern; skip it.
			continue
		}

		recorder.metrics[fieldName] = provider.CustomMetricInfo{
			GroupResource: schema.GroupResource{
				Group:    "",
				Resource: "pods",
			},
			Namespaced: true,
			Metric:     recorder.namer.Register(fieldName),
		}
		recorder.indexedMetrics[fieldName] = MetricMetadata{
			Fields:  *fieldMeta,
			Indices: metricSet.Indices,
		}
	}
	return nil
}

func newRecorder(namer config.Namer) *recorder {
	return &recorder{
		metrics:        make(map[string]provider.CustomMetricInfo),
		indexedMetrics: make(map[string]MetricMetadata),
		namer:          namer,
	}
}

type recorder struct {
	metrics        map[string]provider.CustomMetricInfo
	indexedMetrics map[string]MetricMetadata
	namer          config.Namer
}

// recordStaticFields registers the config-defined static (search-based) metrics
// into the recorder. These fields carry an explicit Search body and are computed
// via a query, so unlike numeric mapping fields they require no _mapping or
// _field_caps lookup to be served. It is shared by periodic discovery and, in
// hpa discovery mode, by client construction (where discoverMetrics never runs),
// so that an HPA referencing a static field can still resolve it.
func recordStaticFields(cfg config.MetricServer, rec *recorder) error {
	for _, metricSet := range cfg.MetricSets {
		for _, field := range metricSet.Fields {
			if len(field.Name) == 0 {
				continue
			}
			search := field.Search
			search.Template = template.Must(template.New("").Parse(search.Body))
			metricResultQuery, err := gojq.Parse(search.MetricPath)
			if err != nil {
				return fmt.Errorf("error while parsing metricResultQuery for field %s: error: %v", field.Name, err)
			}
			search.MetricResultQuery = metricResultQuery
			timestampResultQuery, err := gojq.Parse(search.TimestampPath)
			if err != nil {
				return fmt.Errorf("error while parsing timestampResultQuery for field %s: error: %v", field.Name, err)
			}
			search.TimestampResultQuery = timestampResultQuery
			// This is a static field, save the request body and the metric path.
			rec.indexedMetrics[field.Name] = MetricMetadata{
				Search:  &search,
				Indices: metricSet.Indices,
			}
			rec.metrics[field.Name] = provider.CustomMetricInfo{
				GroupResource: schema.GroupResource{ // TODO: infer resource from configuration
					Group:    "",
					Resource: "pods",
				},
				Namespaced: true,
				// Register with the namer like discovered fields do, so the exposed
				// Metric is the alias and query-time namer.Get resolves it back to the
				// field name. Without this, a static field under a rename config would
				// fail Get with "alias not found".
				Metric: rec.namer.Register(field.Name),
			}
		}
	}
	return nil
}

// ResolveCustomMetric checks whether the given metric is exposed by any configured
// metric set on this client. It uses the _field_caps API (server-side filtered to
// numeric types) which returns a much smaller payload than _mapping.
//
// On success, the metric is registered in the client's internal maps so subsequent
// value queries via GetMetricByName / GetMetricBySelector can serve it without
// re-querying ES.
//
// Metric sets are probed last-configured first and the first hit wins. That
// matches full mode, where a later metric set overwrites an earlier one, so the
// same metric set serves a metric in both modes. Probing continues past a
// metric set that errors, so a failing index pattern does not shadow another
// metric set that serves the metric. The last error is returned only when no
// metric set resolved it.
func (mc *MetricsClient) ResolveCustomMetric(ctx context.Context, metricName string) (provider.CustomMetricInfo, bool, error) {
	// Fast path: already known. Entries here outlive registry.Withdraw (the
	// registry clears its own tables but not this cache), so a re-referenced
	// metric resolves with no ES call. Bounded by distinct HPA-referenced names.
	mc.lock.RLock()
	if info, ok := mc.metrics[metricName]; ok {
		mc.lock.RUnlock()
		return info, true, nil
	}
	mc.lock.RUnlock()

	var lastErr error
	metricSets := mc.metricServerCfg.MetricSets
	for i := len(metricSets) - 1; i >= 0; i-- {
		metricSet := metricSets[i]
		// Skip metric sets whose configured patterns wouldn't accept this name.
		fields := metricSet.Fields.FindMetadata(metricName)
		if fields == nil {
			continue
		}

		found, err := mc.fieldExistsAsNumeric(ctx, metricSet.Indices, metricName)
		if err != nil {
			// Another metric set may still serve the metric; remember the error
			// and surface it only if none does.
			lastErr = err
			continue
		}
		if !found {
			continue
		}

		mc.lock.Lock()
		// Recheck after acquiring write lock; another goroutine may have raced us.
		if info, ok := mc.metrics[metricName]; ok {
			mc.lock.Unlock()
			return info, true, nil
		}
		info := provider.CustomMetricInfo{
			GroupResource: schema.GroupResource{Group: "", Resource: "pods"},
			Namespaced:    true,
			Metric:        mc.namer.Register(metricName),
		}
		mc.metrics[metricName] = info
		mc.indexedMetrics[metricName] = MetricMetadata{
			Fields:  *fields,
			Indices: metricSet.Indices,
		}
		mc.lock.Unlock()
		return info, true, nil
	}

	return provider.CustomMetricInfo{}, false, lastErr
}

// fieldExistsAsNumeric reports whether metricName exists as a numeric field in
// the given index pattern, using a single-field _field_caps lookup.
func (mc *MetricsClient) fieldExistsAsNumeric(ctx context.Context, indices []string, metricName string) (bool, error) {
	fields, err := mc.fieldCaps(ctx, indices, []string{metricName}, fieldCapsLookupTimeout)
	if err != nil {
		return false, err
	}
	typesForField, ok := fields[metricName]
	if !ok {
		return false, nil
	}
	return hasNumericType(typesForField), nil
}
