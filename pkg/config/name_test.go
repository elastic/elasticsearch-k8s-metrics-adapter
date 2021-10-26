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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAliases_Alias(t *testing.T) {
	type want struct {
		alias string
		ok    bool
	}
	tests := []struct {
		name               string
		matches            *Matches
		originalMetricName string
		want               want
	}{
		{
			matches: &Matches{
				Matches: "^(.*)$",
				As:      "${1}@server1",
			},
			originalMetricName: "my_metric",
			want: want{
				alias: "my_metric@server1",
				ok:    true,
			},
		},
		{
			name: "does not match, keep the original",
			matches: &Matches{
				Matches: "^f(.*)$",
				As:      "${1}@server1",
			},
			originalMetricName: "my_metric",
			want: want{
				alias: "my_metric",
				ok:    true,
			},
		},
		{
			name:               "identity",
			matches:            nil,
			originalMetricName: "my_metric",
			want: want{
				alias: "my_metric",
				ok:    true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := NewNamer(tt.matches)
			assert.NoError(t, err)
			// Add the alias
			assert.Equal(t, tt.want.alias, a.Register(tt.originalMetricName))
			// Get the alias
			original, ok := a.Get(tt.want.alias)
			assert.Equal(t, tt.want.ok, ok)
			if tt.want.ok {
				assert.Equal(t, tt.originalMetricName, original)
			}
		})
	}
}
