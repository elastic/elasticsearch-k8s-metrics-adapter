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
	"encoding/json"
	"io"
	"os"
	"path"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"

	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/config"
)

func Test_recorder_processMappingDocument(t *testing.T) {
	testConfig, err := config.From(
		[]byte(`
metricServers:
  - name: k8s-region-observability-cluster
    serverType: elasticsearch
    metricSets:
      - indices: [ '*' ]
`),
	)
	if err != nil {
		panic(err)
	}
	type args struct {
		mapping interface{}
		fields  config.FieldsSet
		indices []string
	}
	tests := []struct {
		name        string
		args        args
		wantMetrics []string
	}{
		{
			args: args{
				mapping: mustReadMapping(path.Join("testdata", "mapping.json")),
				fields:  testConfig.MetricServers[0].MetricSets[0].Fields,
				indices: testConfig.MetricServers[0].MetricSets[0].Indices,
			},
			wantMetrics: []string{
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
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			noopNamer, err := config.NewNamer(nil)
			assert.NoError(t, err)
			metricRecorder := newRecorder(noopNamer)
			metricRecorder.processMappingDocument(tt.args.mapping, tt.args.fields, tt.args.indices)
			sortedResult := make([]string, 0, len(metricRecorder.metrics))
			for metric := range metricRecorder.metrics {
				sortedResult = append(sortedResult, metric)
			}
			sort.Strings(sortedResult)
			assert.Empty(t, cmp.Diff(tt.wantMetrics, sortedResult))
		})
	}
}

func mustReadMapping(file string) interface{} {
	f, err := os.Open(file)
	if err != nil {
		panic(err)
	}
	d, err := io.ReadAll(f)
	if err != nil {
		panic(err)
	}
	i := map[string]interface{}{}
	if err := json.Unmarshal(d, &i); err != nil {
		panic(err)
	}
	mapping, hasMapping := i["mappings"]
	if !hasMapping {
		panic("mo mapping in test file")
	}
	return mapping
}
