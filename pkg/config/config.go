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
	"io/ioutil"
	"regexp"
	"text/template"

	"github.com/itchyny/gojq"
	"gopkg.in/yaml.v2"
	"k8s.io/klog/v2"
)

const configPath = "config/config.yml"

// ObjectSelector defines a reference to a Kubernetes object.
type ObjectSelector struct {
	// Name of the Kubernetes object.
	Name string `json:"name"`
	// Namespace of the Kubernetes object. If empty, defaults to the current namespace.
	Namespace string `json:"namespace,omitempty"`
}

// IsDefined checks if the object selector is not nil and has a name.
// Namespace is not mandatory as it may be inherited by the parent object.
func (o *ObjectSelector) IsDefined() bool {
	return o != nil && (o.Name != "" || o.Namespace != "")
}

func (o *HTTPClientConfig) IsDefined() bool {
	return o != nil
}

type MetricServer struct {
	Name         string           `yaml:"name"`
	Type         string           `yaml:"type"`
	ClientConfig HTTPClientConfig `yaml:"clientConfig,omitempty"`
	MetricSets   MetricSets       `yaml:"metricSets,omitempty"` // only valid if type is elasticsearch
	Rename       *Matches         `yaml:"rename,omitempty"`
	Priority     int              `yaml:"-"`
}

type Matches struct {
	Matches string `yaml:"matches"`
	As      string `yaml:"as"`
}

type ReadinessProbe struct {
	FailureThreshold int `yaml:"failureThreshold"`
}

type MetricServers []MetricServer

type Config struct {
	ReadinessProbe ReadinessProbe `yaml:"failureThreshold"`
	MetricServers  []MetricServer `yaml:"metricServers"`
	Upstream       *MetricServer  `yaml:"-"`
	Elasticsearch  MetricServer   `yaml:"-"`
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

var defaultFieldSet = Fields{
	Patterns: []string{"^.*$"},
}

func (f FieldsSet) FindMetadata(fieldName string) *Fields {
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
	klog.Infof("Reading adapter configuration from %s", configPath)
	config, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	return From(config)
}

func From(source []byte) (*Config, error) {
	config := &Config{}
	// Read file as yaml
	err := yaml.Unmarshal(source, config)
	if err != nil {
		return nil, err
	}

	// Set priority given the position in the array
	for i := range config.MetricServers {
		config.MetricServers[i].Priority = i
	}

	// Ensure that configuration is valid.
	checkOrDie(config)

	// Compile the regular expressions
	for i := range config.Elasticsearch.MetricSets {
		if len(config.Elasticsearch.MetricSets[i].Fields) == 0 {
			// User did not provide a field pattern, we assume the user wants all numeric fields in this index to be available.
			config.Elasticsearch.MetricSets[i].Fields = append(config.Elasticsearch.MetricSets[i].Fields, defaultFieldSet)
		}
		metricSet := config.Elasticsearch.MetricSets[i]
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
	return config, nil
}

func checkOrDie(config *Config) {
	var hasElasticsearch, hasUpstream bool
	for i, server := range config.MetricServers {
		if server.Rename != nil {
			if len(server.Rename.Matches) == 0 || len(server.Rename.As) == 0 {
				klog.Fatalf("%s: rename directive must contain both \"matches\" and \"as\" fields", server.Name)
			}
		}
		switch server.Type {
		case "custom":
			if hasUpstream {
				klog.Fatalf("only one upstream custom metric server is allowed")
			}
			if !server.ClientConfig.IsDefined() {
				klog.Fatalf("%s: HTTP client configuration is not defined in upstream custom metric server", server.Name)
			}
			if len(server.MetricSets) > 0 {
				klog.Fatalf("%s: metricSets is not allowed in upstream custom metric server", server.Name)
			}
			hasUpstream = true
			upstreamCfg := config.MetricServers[i]
			config.Upstream = &upstreamCfg
		case "elasticsearch":
			if hasElasticsearch {
				klog.Fatalf("%s: only one Elasticsearch instance is allowed", server.Name)
			}
			if len(server.MetricSets) == 0 {
				klog.Warningf("%s: no metricSets defined", server.Name)
			}
			if !server.ClientConfig.IsDefined() {
				klog.Fatalf("%s: Elasticsearch requires clientConfig to be set", server.Name)
			}
			hasElasticsearch = true
			config.Elasticsearch = config.MetricServers[i]
		default:
			klog.Fatalf("%s: unknown metric server type: %s", server.Type, server.Name)
		}

	}
}
