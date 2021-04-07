// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. Elasticsearch B.V. licenses this file to
// you under the Apache License, Version 2.0 (the "License");
// you may  not use this file except in compliance with the
// License.
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

package config

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"text/template"
	"time"

	esv7 "github.com/elastic/go-elasticsearch/v7"
	"github.com/elastic/go-elasticsearch/v7/esapi"
	"github.com/itchyny/gojq"
	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"

	"gopkg.in/yaml.v2"
)

//go:embed default.yaml
var defaultConfig string

type Config struct {
	MetricSets MetricSets `yaml:"metricSets"`
}

type MetricSets []MetricSet

type MetricSet struct {
	// Comma separated set of Indices
	Indices []string `yaml:"indices"`
	// Fields exposed within the Indices
	Fields FieldsSet `yaml:"fields"`
}

type FieldsSet []Fields
type Fields struct {
	// Filter which fields are exposed, for example { "^prometheus\.metrics\." }
	Patterns         []string        `yaml:"patterns"`
	compiledPatterns []regexp.Regexp `yaml:"-"`
	// Name is the name of a static field
	Name string `yaml:"name"`
	// Search is the search associated to the static field
	Search Search `yaml:"search"`
	// Help to determine which fields are labels, for example ^prometheus\.labels\.(.*)
	Labels []string `yaml:"labels"`
	// Resource associated with the metrics, default is {group: "", resource: "pod"}
	Resources GroupResource
}

type Search struct {
	// MetricPath is the path to be used to get the result from the search
	MetricPath string `yaml:"metricPath"`
	// TimestampPath is the path to be used to get the result timestamp
	TimestampPath string `yaml:"timestampPath"`
	// Body is the body to be used to search the metric.
	Body string `json:"body"`
	// Template is the template version of the body
	Template *template.Template `yaml:"-"`
	// MetricResultQuery is the template version of metricPath
	MetricResultQuery *gojq.Query `yaml:"-"`
	// TimestampResultQuery is the template version of metricPath
	TimestampResultQuery *gojq.Query `yaml:"-"`
}

func (f FieldsSet) findMetadata(fieldName string) *Fields {
	for _, fieldSet := range f {
		for _, pattern := range fieldSet.compiledPatterns {
			if pattern.MatchString(fieldName) {
				return &fieldSet
			}
		}
	}
	return nil
}

type GroupResource struct {
	Group    string `yaml:"group"`
	Resource string `yaml:"resource"`
}

func Default() (*Config, error) {
	return From(defaultConfig)
}

func From(source string) (*Config, error) {
	config := Config{}
	// Read file as yaml
	err := yaml.Unmarshal([]byte(source), &config)
	if err != nil {
		return nil, err
	}
	// Compile the regular expressions
	for i := range config.MetricSets {
		metricSet := config.MetricSets[i]
		for j := range metricSet.Fields {
			field := metricSet.Fields[j]
			metricSet.Fields[j].compiledPatterns = make([]regexp.Regexp, len(field.Patterns))
			for k, pattern := range field.Patterns {
				compiledPattern, err := regexp.Compile(pattern)
				if err != nil {
					return nil, err
				}
				metricSet.Fields[j].compiledPatterns[k] = *compiledPattern
			}
		}
	}

	return &config, nil
}

func (c *Config) Metrics(esClient *esv7.Client) ([]provider.CustomMetricInfo, map[string]MetricMetadata, error) {
	metricRecoder := newRecorder()

	// We first record static fields, they do not require to read the mapping
	for _, metricSet := range c.MetricSets {
		for _, field := range metricSet.Fields {
			if len(field.Name) > 0 {
				search := field.Search
				search.Template = template.Must(template.New("").Parse(search.Body))
				metricResultQuery, err := gojq.Parse(search.MetricPath)
				if err != nil {
					klog.Fatalf("Error while parsing metricResultQuery for field %s: error: %v", field.Name, err)

				}
				search.MetricResultQuery = metricResultQuery
				timestampResultQuery, err := gojq.Parse(search.TimestampPath)
				if err != nil {
					klog.Fatalf("Error while parsing timestampResultQuery for field %s: error: %v", field.Name, err)

				}
				search.TimestampResultQuery = timestampResultQuery
				// This is a static field, save the request body and the metric path
				metricRecoder.indexedMetrics[field.Name] = MetricMetadata{
					Search:  &search,
					Indices: metricSet.Indices,
				}
				metricRecoder.metrics = append(metricRecoder.metrics, provider.CustomMetricInfo{
					GroupResource: schema.GroupResource{ // TODO: infer resource from configuration
						Group:    "",
						Resource: "pod",
					},
					Namespaced: true,
					Metric:     field.Name,
				})
			}
		}
	}

	for _, metricSet := range c.MetricSets {
		if err := getMappingFor(metricSet, esClient, metricRecoder); err != nil {
			return nil, nil, err
		}
	}
	return metricRecoder.metrics, metricRecoder.indexedMetrics, nil
}

func getMappingFor(metricSet MetricSet, esClient *esv7.Client, recorder *recorder) error {
	req := esapi.IndicesGetMappingRequest{Index: metricSet.Indices}
	res, err := req.Do(context.Background(), esClient)
	if err != nil {
		return fmt.Errorf("discevery error, got response: %s", err)
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

func (r *recorder) processMappingDocument(mapping interface{}, fields FieldsSet, indices []string) {
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

func newRecorder() *recorder {
	return &recorder{
		indexedMetrics: make(map[string]MetricMetadata),
	}
}

type MetricMetadata struct {
	Fields  Fields
	Search  *Search
	Indices []string
}

type recorder struct {
	metrics        []provider.CustomMetricInfo
	indexedMetrics map[string]MetricMetadata
}

func (r *recorder) _processMappingDocument(root string, d map[string]interface{}, fieldsSet FieldsSet, indices []string) {
	for k, t := range d {
		if k == "*" {
			return
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
				// New metric
				metricName := fmt.Sprintf("%s.%s", root, k)
				if strings.HasPrefix(metricName, "prometheus") {
					fmt.Println("--")
				}
				fields := fieldsSet.findMetadata(metricName)
				if fields == nil {
					// field does not match a pattern, do not register it as available
					continue
				}
				r.metrics = append(r.metrics, provider.CustomMetricInfo{
					GroupResource: schema.GroupResource{ // TODO: infer resource from configuration
						Group:    "",
						Resource: "pod",
					},
					Namespaced: true,
					Metric:     metricName,
				})
				r.indexedMetrics[metricName] = MetricMetadata{
					Fields:  *fields,
					Indices: indices,
				}
			}
		}
	}
}

type MetricLister struct {
	cfg            *Config
	esClient       *esv7.Client
	once           sync.Once
	currentMetrics []provider.CustomMetricInfo
	m              sync.RWMutex
	metadata       map[string]MetricMetadata
}

func NewMetricLister(cfg *Config, esClient *esv7.Client) *MetricLister {
	return &MetricLister{
		cfg:            cfg,
		esClient:       esClient,
		once:           sync.Once{},
		currentMetrics: nil,
		m:              sync.RWMutex{},
	}
}

func (ml *MetricLister) GetMetricMetadata(metric string) *MetricMetadata {
	ml.m.RLock()
	defer ml.m.RUnlock()
	metadata, exists := ml.metadata[metric]
	if !exists {
		return nil
	}
	return &metadata
}

func (ml *MetricLister) GetMetrics() []provider.CustomMetricInfo {
	ml.m.RLock()
	defer ml.m.RUnlock()
	return ml.currentMetrics
}

func (ml *MetricLister) Start() {
	ml.once.Do(
		func() {
			ml.m.Lock()
			defer ml.m.Unlock()
			attempts := 10
			sleep := 5 * time.Second
			for i := 0; ; i++ {
				klog.Infof("Fetching metric list from Elasticsearch, attempt %i", i)
				metrics, metadata, err := ml.cfg.Metrics(ml.esClient)
				if err == nil {
					ml.currentMetrics = metrics
					ml.metadata = metadata
					return
				}
				if i >= (attempts - 1) {
					break
				}
				klog.Errorf("Error while fetching metrics : %v, will retry in %s", err, sleep)
				time.Sleep(sleep)
			}
			klog.Fatalf("Give up after %d attempts", attempts)
		},
	)
}
