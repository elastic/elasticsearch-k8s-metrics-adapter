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
	"fmt"
	"strings"
	"text/template"

	"github.com/go-logr/logr"
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

func isTypeAllowed(t string) bool {
	_, ok := numericTypesSet[t]
	return ok
}

// fieldTypes is the per-field slice of a _field_caps response: a map from ES
// type name to that type's capabilities. Only the type name matters to us, so
// the value struct carries just "type".
type fieldTypes = map[string]struct {
	Type string `json:"type"`
}

// fieldCaps maps a field name to its fieldTypes. It is the shape decoded from
// the "fields" object of a _field_caps response.
type fieldCaps = map[string]fieldTypes

// fetchNumericFieldCaps runs a _field_caps request for the given fields against
// the index pattern and returns the decoded "fields" map. It is shared by
// discoverFieldCaps (fields=["*"]) and fieldExistsAsNumeric (a single field).
//
// The request is filtered server-side to numeric types (Types), tolerates
// missing or empty index patterns (AllowNoIndices/IgnoreUnavailable), and uses
// filter_path=fields to drop the top-level "indices" array from the response.
// For an index pattern like metrics-* that matches thousands of data-stream
// backing indices, that array dominates the payload even though we only care
// about field types.
func fetchNumericFieldCaps(ctx context.Context, esClient *esv8.Client, indices, fields []string) (fieldCaps, error) {
	req := esapi.FieldCapsRequest{
		Index:             indices,
		Fields:            fields,
		Types:             numericTypes,
		AllowNoIndices:    ptr.To(true),
		IgnoreUnavailable: ptr.To(true),
		FilterPath:        []string{"fields"},
	}
	res, err := req.Do(ctx, esClient)
	if err != nil {
		return nil, fmt.Errorf("_field_caps request failed: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
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
func hasNumericType(types fieldTypes) bool {
	for t := range types {
		if isTypeAllowed(t) {
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
		if err := discoverFieldCaps(mc.logger, metricSet, mc.Client, metricRecorder); err != nil {
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
// to numeric types, and is ~5x smaller on the wire (see fetchNumericFieldCaps).
func discoverFieldCaps(logger logr.Logger, metricSet config.MetricSet, esClient *esv8.Client, recorder *recorder) error {
	fields, err := fetchNumericFieldCaps(context.Background(), esClient, metricSet.Indices, []string{"*"})
	if err != nil {
		return err
	}
	if len(fields) == 0 {
		logger.Info("No numeric fields found", "index_pattern", strings.Join(metricSet.Indices, ","))
		return nil
	}

	logger.V(1).Info("Discovered fields via _field_caps",
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
				Metric:     field.Name,
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
func (mc *MetricsClient) ResolveCustomMetric(ctx context.Context, metricName string) (provider.CustomMetricInfo, bool, error) {
	// Fast path: already known.
	mc.lock.RLock()
	if info, ok := mc.metrics[metricName]; ok {
		mc.lock.RUnlock()
		return info, true, nil
	}
	mc.lock.RUnlock()

	for _, metricSet := range mc.metricServerCfg.MetricSets {
		// Skip metric sets whose configured patterns wouldn't accept this name.
		fields := metricSet.Fields.FindMetadata(metricName)
		if fields == nil {
			continue
		}

		found, err := fieldExistsAsNumeric(ctx, mc.Client, metricSet.Indices, metricName)
		if err != nil {
			return provider.CustomMetricInfo{}, false, err
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

	return provider.CustomMetricInfo{}, false, nil
}

// fieldExistsAsNumeric reports whether metricName exists as a numeric field in
// the given index pattern, using a single-field _field_caps lookup.
func fieldExistsAsNumeric(ctx context.Context, esClient *esv8.Client, indices []string, metricName string) (bool, error) {
	fields, err := fetchNumericFieldCaps(ctx, esClient, indices, []string{metricName})
	if err != nil {
		return false, err
	}
	typesForField, ok := fields[metricName]
	if !ok {
		return false, nil
	}
	return hasNumericType(typesForField), nil
}

