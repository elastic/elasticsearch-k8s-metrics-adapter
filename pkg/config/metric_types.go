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

package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

const (
	CustomMetricType   MetricType = "custom"
	ExternalMetricType MetricType = "external"
)

type MetricType string
type MetricTypes []MetricType

func (mt *MetricTypes) HasType(metricType MetricType) bool {
	if mt == nil || len(*mt) == 0 {
		// By default, we consider that a metric server serves all the known metric types.
		return true
	}
	for _, t := range *mt {
		if t == metricType {
			return true
		}
	}
	return false
}

func (mt *MetricTypes) UnmarshalYAML(value *yaml.Node) error {
	if value == nil {
		return nil
	}
	if value.Kind != yaml.SequenceNode {
		return fmt.Errorf("unable to unmarshal metric type: %+v", value)
	}

	for _, t := range value.Content {
		if t.Value != string(CustomMetricType) && t.Value != string(ExternalMetricType) {
			return fmt.Errorf("unknown metric type: %s", t.Value)
		}
		*mt = append(*mt, MetricType(t.Value))
	}
	return nil
}
