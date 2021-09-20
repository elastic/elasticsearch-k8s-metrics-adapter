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
	_ "embed"
	"io/ioutil"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFrom(t *testing.T) {
	type args struct {
		file string
	}
	tests := []struct {
		name              string
		args              args
		wantUpstream      MetricServer
		wantElasticsearch MetricServer
		wantErr           bool
	}{
		{
			name: "Happy path 1",
			args: args{file: "config1.yaml"},
			wantUpstream: MetricServer{
				Name:       "my-existing-metrics-adapter",
				ServerType: "custom",
				Priority:   0,
				ClientConfig: HTTPClientConfig{
					Host: "https://custom-metrics-apiserver.custom-metrics.svc",
					AuthenticationConfig: &AuthenticationConfig{
						BearerTokenFile: "/run/secrets/kubernetes.io/serviceaccount/token",
					},
					TLSClientConfig: &TLSClientConfig{
						Insecure: false,
						CAFile:   "/run/secrets/kubernetes.io/serviceaccount/ca.crt",
					},
				},
			},
			wantElasticsearch: MetricServer{
				Name:       "elasticsearch-metrics-cluster",
				ServerType: "elasticsearch",
				Priority:   1,
				ClientConfig: HTTPClientConfig{
					Host: "https://elasticsearch-es-http.default.svc:9200",
					AuthenticationConfig: &AuthenticationConfig{
						Username: "elastic",
						Password: "${PASSWORD}",
					},
					TLSClientConfig: &TLSClientConfig{
						Insecure: false,
						CAFile:   "/mnt/elastic-internal/elasticsearch-association/default/elasticsearch/certs/ca.crt",
					},
				},
				Rename: &Matches{
					Matches: "^(.*)$",
					As:      "${1}@elasticsearch-metrics-cluster",
				},
				MetricTypes: newMetricTypes("custom"),
				MetricSets: MetricSets{
					MetricSet{
						Indices: []string{"metrics-*"},
						Fields: []Fields{
							{
								Patterns:         []string{`^.*$`}, // Automatically added when Patterns array is empty
								compiledPatterns: []regexp.Regexp{*regexp.MustCompile(`^.*$`)},
							},
						},
					},
					MetricSet{
						Indices: []string{"metricbeat-*"},
						Fields: []Fields{
							{
								Patterns:         []string{`^.*$`},
								compiledPatterns: []regexp.Regexp{*regexp.MustCompile(`^.*$`)},
							},
							{
								Patterns:         []string{`^kibana\.stats\.`},
								compiledPatterns: []regexp.Regexp{*regexp.MustCompile(`^kibana\.stats\.`)},
							},
							{
								Name:             "kibana.stats.load.pod",
								compiledPatterns: []regexp.Regexp{},
								Search: Search{
									MetricPath:    ".aggregations.custom_name.buckets.[0].pod_load.value",
									TimestampPath: ".aggregations.custom_name.buckets.[0].timestamp.value_as_string",
									Body:          "{\n  \"query\": { \"my query\" }\n}\n",
								},
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfgAsBytes, err := ioutil.ReadFile(filepath.Join("testdata", tt.args.file))
			got, err := From(cfgAsBytes)
			if (err != nil) != tt.wantErr {
				t.Errorf("From() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.Equal(t, tt.wantElasticsearch, getMetricServer(t, "elasticsearch-metrics-cluster", got))
			assert.Equal(t, tt.wantUpstream, getMetricServer(t, "my-existing-metrics-adapter", got))
		})
	}
}

func getMetricServer(t *testing.T, name string, config *Config) MetricServer {
	t.Helper()
	for _, ms := range config.MetricServers {
		if ms.Name == name {
			return ms
		}
	}
	t.Fatalf("Metric server not found: %s", name)
	return MetricServer{}
}

func newMetricTypes(metricTypes ...string) *MetricTypes {
	result := make(MetricTypes, len(metricTypes))
	for i := range metricTypes {
		result[i] = MetricType(metricTypes[i])
	}
	return &result
}
