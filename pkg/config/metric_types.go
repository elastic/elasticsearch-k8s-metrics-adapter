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
