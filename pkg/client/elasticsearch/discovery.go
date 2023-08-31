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

	esv8 "github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/itchyny/gojq"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/custom-metrics-apiserver/pkg/provider"

	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/config"
)

var allowedTypes = map[string]struct{}{
	"byte":          {},
	"double":        {},
	"float":         {},
	"half_float":    {},
	"integer":       {},
	"long":          {},
	"scaled_float":  {},
	"short":         {},
	"unsigned_long": {},
}

func isTypeAllowed(t string) bool {
	_, ok := allowedTypes[t]
	return ok
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

	// We first record static fields, they do not require to read the mapping
	for _, metricSet := range mc.metricServerCfg.MetricSets {
		for _, field := range metricSet.Fields {
			if len(field.Name) > 0 {
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
				// This is a static field, save the request body and the metric path
				metricRecorder.indexedMetrics[field.Name] = MetricMetadata{
					Search:  &search,
					Indices: metricSet.Indices,
				}
				metricRecorder.metrics[field.Name] = provider.CustomMetricInfo{
					GroupResource: schema.GroupResource{ // TODO: infer resource from configuration
						Group:    "",
						Resource: "pods",
					},
					Namespaced: true,
					Metric:     field.Name,
				}
			}
		}
	}

	for _, metricSet := range mc.metricServerCfg.MetricSets {
		if err := getMappingFor(metricSet, mc.Client, metricRecorder); err != nil {
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

func getMappingFor(metricSet config.MetricSet, esClient *esv8.Client, recorder *recorder) error {
	req := esapi.IndicesGetMappingRequest{Index: metricSet.Indices}
	res, err := req.Do(context.Background(), esClient)
	if err != nil {
		return fmt.Errorf("discovery error, got response: %s", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("[%s] Error getting index mapping %v", res.Status(), metricSet.Indices)
	} else {
		// Deserialize the response into a map.
		var r map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
			return fmt.Errorf("error parsing the response body: %s", err)
		} else {
			if len(r) == 0 {
				logger.Info("Mapping is empty", "index_pattern", strings.Join(metricSet.Indices, ","))
				return nil
			}
			// Process mapping
			for _, indexMapping := range r {
				m := indexMapping.(map[string]interface{})
				mapping, hasMapping := m["mappings"]
				if !hasMapping {
					return fmt.Errorf("discovery error: no 'mapping' field in %s", metricSet.Indices)
				}
				recorder.processMappingDocument(mapping, metricSet.Fields, metricSet.Indices)
			}
		}
	}
	return nil
}

func (r *recorder) processMappingDocument(mapping interface{}, fields config.FieldsSet, indices []string) {
	tm, ok := mapping.(map[string]interface{})
	if !ok {
		return
	}
	rp := tm["properties"]
	rpm, ok := rp.(map[string]interface{})
	if !ok {
		return
	}
	r._processMappingDocument("", rpm, fields, indices)
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

func (r *recorder) _processMappingDocument(root string, d map[string]interface{}, fieldsSet config.FieldsSet, indices []string) {
	for k, t := range d {
		if k == "*" {
			continue
		}
		if k == "properties" {
			tm, ok := t.(map[string]interface{})
			if !ok {
				continue
			}
			r._processMappingDocument(root, tm, fieldsSet, indices)
		} else {
			// Is there a properties child ?
			child, ok := t.(map[string]interface{})
			if !ok {
				continue
			}
			if _, hasProperties := child["properties"]; hasProperties {
				var newRoot string
				if root == "" {
					newRoot = k
				} else {
					newRoot = fmt.Sprintf("%s.%s", root, k)
				}
				r._processMappingDocument(newRoot, child, fieldsSet, indices)
			} else {
				// Ensure that we have a type
				if t, hasType := child["type"]; !(hasType && isTypeAllowed(t.(string))) {
					continue
				}
				metricName := ""
				// New metric
				if root == "" {
					metricName = k
				} else {
					metricName = fmt.Sprintf("%s.%s", root, k)
				}

				fields := fieldsSet.FindMetadata(metricName)
				if fields == nil {
					// field does not match a pattern, do not register it as available
					continue
				}
				r.metrics[metricName] = provider.CustomMetricInfo{
					GroupResource: schema.GroupResource{ // TODO: infer resource from configuration
						Group:    "",
						Resource: "pods",
					},
					Namespaced: true,
					Metric:     r.namer.Register(metricName),
				}
				r.indexedMetrics[metricName] = MetricMetadata{
					Fields:  *fields,
					Indices: indices,
				}
			}
		}
	}
}
