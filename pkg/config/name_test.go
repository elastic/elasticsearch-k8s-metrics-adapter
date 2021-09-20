package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAliases_Alias(t *testing.T) {
	type fields struct {
		matches string
		as      string
	}
	type want struct {
		metric string
		ok     bool
	}
	tests := []struct {
		name   string
		fields fields
		source string
		want   want
	}{
		{
			fields: fields{
				matches: "^(.*)$",
				as:      "${1}@server1",
			},
			source: "my_metric",
			want: want{
				metric: "my_metric@server1",
				ok:     true,
			},
		},
		{
			name: "does ot match, keep the original",
			fields: fields{
				matches: "^(.*)$",
				as:      "${1}@server1",
			},
			source: "my_metric",
			want: want{
				metric: "my_metric@server1",
				ok:     true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := NewNamer(tt.fields.matches, tt.fields.as)
			assert.NoError(t, err)
			// Add the alias
			a.Register(tt.source)
			// Get the alias
			original, ok := a.Get("my_metric@server1")
			assert.Equal(t, tt.want.ok, ok)
			assert.Equal(t, tt.source, original)
		})
	}
}
