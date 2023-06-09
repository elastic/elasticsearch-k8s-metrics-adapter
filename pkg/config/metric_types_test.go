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

package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"

	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/config"
)

func TestMetricTypes_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		name             string
		data             []byte
		wantCustomType   bool
		wantExternalType bool
		wantErr          bool
	}{
		{
			name:             "Both types",
			data:             []byte(`["custom", "external"]`),
			wantCustomType:   true,
			wantExternalType: true,
			wantErr:          false,
		},
		{
			name:             "Default",
			data:             nil,
			wantCustomType:   true,
			wantExternalType: true,
			wantErr:          false,
		},
		{
			name:             "Only custom",
			data:             []byte(`["custom"]`),
			wantCustomType:   true,
			wantExternalType: false,
			wantErr:          false,
		},
		{
			name:             "Only external",
			data:             []byte(`[ "external"]`),
			wantCustomType:   false,
			wantExternalType: true,
			wantErr:          false,
		},
		{
			name:             "Unknown metric type",
			data:             []byte(`[ "foo"]`),
			wantCustomType:   false,
			wantExternalType: false,
			wantErr:          true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := &config.MetricTypes{}
			// Read file as yaml
			err := yaml.Unmarshal(tt.data, mt)
			if (err != nil) != tt.wantErr {
				t.Errorf("MetricTypes.IsValid() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil {
				assert.Equal(t, tt.wantCustomType, mt.HasType(config.CustomMetricType))
				assert.Equal(t, tt.wantExternalType, mt.HasType(config.ExternalMetricType))
			}
		})
	}
}
