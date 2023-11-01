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
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	apierr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/custom-metrics-apiserver/pkg/provider"
)

func Test_getMetricDocument(t *testing.T) {
	type args struct {
		info           provider.CustomMetricInfo
		name           types.NamespacedName
		metricSelector labels.Selector
		doc            map[string]interface{}
	}
	tests := []struct {
		name       string
		args       args
		wantResult map[string]interface{}
		wantErr    error
	}{
		{
			name: "nil doc",
			args: args{
				info:           provider.CustomMetricInfo{GroupResource: schema.GroupResource{Group: "g1", Resource: "r1"}, Metric: "m1"},
				name:           types.NamespacedName{Namespace: "ns1", Name: "name1"},
				metricSelector: labels.Set{"k1": "v1"}.AsSelector(),
				doc:            nil,
			},
			wantResult: nil,
			wantErr:    newStatusError("the server could not find the metric m1 for r1.g1 name1 with selector k1=v1"),
		},
		{
			name: "doc with mistyped hits",
			args: args{
				info:           provider.CustomMetricInfo{GroupResource: schema.GroupResource{Group: "g1", Resource: "r1"}, Metric: "m1"},
				name:           types.NamespacedName{Namespace: "ns1", Name: "name1"},
				metricSelector: labels.Set{"k1": "v1"}.AsSelector(),
				doc: map[string]interface{}{
					"hits": "badtype",
				},
			},
			wantResult: nil,
			wantErr:    errors.New("cannot convert hits: badtype"),
		},
		{
			name: "doc with mistyped hits.hits",
			args: args{
				info:           provider.CustomMetricInfo{GroupResource: schema.GroupResource{Group: "g1", Resource: "r1"}, Metric: "m1"},
				name:           types.NamespacedName{Namespace: "ns1", Name: "name1"},
				metricSelector: labels.Set{"k1": "v1"}.AsSelector(),
				doc: map[string]interface{}{
					"hits": map[string]interface{}{
						"hits": "badtype",
					},
				},
			},
			wantResult: nil,
			wantErr:    errors.New("cannot convert docs: badtype"),
		},
		{
			name: "doc with mistyped hits.hits[0]",
			args: args{
				info:           provider.CustomMetricInfo{GroupResource: schema.GroupResource{Group: "g1", Resource: "r1"}, Metric: "m1"},
				name:           types.NamespacedName{Namespace: "ns1", Name: "name1"},
				metricSelector: labels.Set{"k1": "v1"}.AsSelector(),
				doc: map[string]interface{}{
					"hits": map[string]interface{}{
						"hits": []interface{}{"badtype"},
					},
				},
			},
			wantResult: nil,
			wantErr:    errors.New("cannot convert document: badtype"),
		},
		{
			name: "happy path",
			args: args{
				info:           provider.CustomMetricInfo{GroupResource: schema.GroupResource{Group: "g1", Resource: "r1"}, Metric: "m1"},
				name:           types.NamespacedName{Namespace: "ns1", Name: "name1"},
				metricSelector: labels.Set{"k1": "v1"}.AsSelector(),
				doc: map[string]interface{}{
					"hits": map[string]interface{}{
						"hits": []interface{}{
							map[string]interface{}{"k": "v"},
						},
					},
				},
			},
			wantResult: map[string]interface{}{"k": "v"},
			wantErr:    nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := getMetricDocument(tt.args.info, tt.args.name, tt.args.metricSelector, tt.args.doc)
			assert.Equal(t, tt.wantResult, result)
			assert.Equal(t, tt.wantErr, err)
		})
	}
}

func newStatusError(msg string) *apierr.StatusError {
	return &apierr.StatusError{ErrStatus: metav1.Status{
		Status:  metav1.StatusFailure,
		Code:    int32(http.StatusNotFound),
		Reason:  metav1.StatusReasonNotFound,
		Message: msg,
	}}
}

func Test_parseResponseBodyError(t *testing.T) {
	type args struct {
		decodedResponseBody map[string]interface{}
	}
	tests := []struct {
		name       string
		args       args
		wantResult map[string]interface{}
		wantErr    error
	}{
		{
			name: "Happy path, valid error struct in response body",
			args: args{
				decodedResponseBody: map[string]interface{}{
					"error": map[string]interface{}{
						"type":   "test",
						"reason": "testing",
					},
				},
			},
			wantResult: map[string]interface{}{
				"type":   "test",
				"reason": "testing",
			},
			wantErr: nil,
		},
		{
			name: "nil error in response body",
			args: args{
				decodedResponseBody: map[string]interface{}{
					"error": nil,
				},
			},
			wantErr: fmt.Errorf("unable to parse error from the response body: %v", map[string]interface{}{
				"error": nil,
			}),
		},
		{
			name: "unexpected format for error in response body",
			args: args{
				decodedResponseBody: map[string]interface{}{
					"error": "test error",
				},
			},
			wantErr: fmt.Errorf("error from response body in unexpected format: %v", "test error"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseResponseBodyError(tt.args.decodedResponseBody)
			assert.Equal(t, tt.wantResult, result)
			assert.Equal(t, tt.wantErr, err)
		})
	}
}
