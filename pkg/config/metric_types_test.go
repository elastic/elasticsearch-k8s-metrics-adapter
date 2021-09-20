package config_test

import (
	"testing"

	"github.com/elastic/elasticsearch-adapter/pkg/config"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
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
