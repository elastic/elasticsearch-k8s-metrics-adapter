/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package registry

import (
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/elastic/elasticsearch-adapter/pkg/client"
	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRegistry_UpdateMetrics(t *testing.T) {
	type fields struct {
		registry *Registry
	}
	type args struct {
		metricsSrc client.Interface
		cms        map[provider.CustomMetricInfo]struct{}
		ems        map[provider.ExternalMetricInfo]struct{}
	}
	tests := []struct {
		name       string
		fields     fields
		args       args
		assertFunc func(t *testing.T, r *Registry)
	}{
		{
			name: "Multi-operation registry test",
			fields: fields{
				registry: newFakeRegistry().
					addExistingCustomMetrics(newFakeMetricsClient("client1", 0), "c_metric1", "metric2", "c_metric3").
					addExistingCustomMetrics(newFakeMetricsClient("client2", 1), "c_metric2", "c_metric3"). // c_metric2 and c_metric3 are also served by client2
					addExternalCustomMetrics(newFakeMetricsClient("client1", 0), "e_metric1", "e_metric3"). // e_metric1 and e_metric3 are already served as external metrics
					registry,
			},
			args: args{
				metricsSrc: newFakeMetricsClient("client1", 0),
				cms:        fakeCustomMetricSet("c_metric2", "c_metric4", "c_metric5", "c_metric6"), // c_metric[4:6] are new external metrics served by client1
				ems:        fakeExternalMetricSet("e_metric1", "e_metric2"),                         // e_metric2 is a new external metric served by client1
			},
			assertFunc: func(t *testing.T, r *Registry) {
				// We are expecting to serve 6 custom metrics: metric[2:6]
				assert.Equal(t, 5, len(r.customMetrics))
				assert.ElementsMatch(
					t,
					[]provider.CustomMetricInfo{
						{Metric: "c_metric2"}, {Metric: "c_metric3"}, {Metric: "c_metric4"}, {Metric: "c_metric5"}, {Metric: "c_metric6"},
					},
					r.ListAllCustomMetrics(),
				)

				// We are expecting to serve 2 external metric: e_metric[1:2]
				assert.Equal(t, 2, len(r.externalMetrics))
				assert.ElementsMatch(
					t,
					[]provider.ExternalMetricInfo{
						{Metric: "e_metric1"}, {Metric: "e_metric2"},
					},
					r.ListAllExternalMetrics(),
				)

				// custom metric2 is served by client2
				c1, err := r.GetCustomMetricClient(provider.CustomMetricInfo{Metric: "c_metric2"})
				assert.NoError(t, err)
				assert.Equal(t, "client2", c1.GetConfiguration().Name, "c_metric2 should be served by client2")

				// custom metricX is served by no metric client.
				c2, err := r.GetCustomMetricClient(provider.CustomMetricInfo{Metric: "metricX"})
				assert.Equal(t, &errors.StatusError{
					ErrStatus: metav1.Status{
						Status:  metav1.StatusFailure,
						Code:    http.StatusNotFound,
						Reason:  metav1.StatusReasonNotFound,
						Message: "custom metric metricX is not served by any metric client",
					}}, err)
				assert.Nil(t, c2)

				// custom metric6 is served by client1
				c6, err := r.GetCustomMetricClient(provider.CustomMetricInfo{Metric: "c_metric6"})
				assert.NoError(t, err)
				assert.Equal(t, "client1", c6.GetConfiguration().Name, "c_metric6 should be served by client1")

				// external metric e_metric1 and e_metric2 are served by client1
				e1, err := r.GetExternalMetricClient(provider.ExternalMetricInfo{Metric: "e_metric1"})
				assert.NoError(t, err)
				assert.Equal(t, "client1", e1.GetConfiguration().Name, "e_metric1 should be  served by client1")
				e2, err := r.GetExternalMetricClient(provider.ExternalMetricInfo{Metric: "e_metric2"})
				assert.NoError(t, err)
				assert.Equal(t, "client1", e2.GetConfiguration().Name, "e_metric2 should be  served by client1")

				// custom metricX is served by no metric client.
				e3, err := r.GetExternalMetricClient(provider.ExternalMetricInfo{Metric: "e_metric3"})
				assert.Equal(t, &errors.StatusError{
					ErrStatus: metav1.Status{
						Status:  metav1.StatusFailure,
						Code:    http.StatusNotFound,
						Reason:  metav1.StatusReasonNotFound,
						Message: "external metric e_metric3 is not served by any metric client",
					}}, err)
				assert.Nil(t, e3)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.fields.registry.UpdateMetrics(tt.args.metricsSrc, tt.args.cms, tt.args.ems)
			tt.assertFunc(t, tt.fields.registry)
		})
	}
}

func TestRegistry_GetCustomMetricsSource(t *testing.T) {
	type fields struct {
		lock            sync.RWMutex
		customMetrics   map[provider.CustomMetricInfo]*metricClients
		externalMetrics map[provider.ExternalMetricInfo]*metricClients
	}
	type args struct {
		info provider.CustomMetricInfo
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *client.Interface
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Registry{
				lock:            tt.fields.lock,
				customMetrics:   tt.fields.customMetrics,
				externalMetrics: tt.fields.externalMetrics,
			}
			got, err := r.GetCustomMetricClient(tt.args.info)
			if (err != nil) != tt.wantErr {
				t.Errorf("Registry.GetCustomMetricClient() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Registry.GetCustomMetricClient() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRegistry_GetExternalMetricsSource(t *testing.T) {
	type fields struct {
		lock            sync.RWMutex
		customMetrics   map[provider.CustomMetricInfo]*metricClients
		externalMetrics map[provider.ExternalMetricInfo]*metricClients
	}
	type args struct {
		info provider.ExternalMetricInfo
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *client.Interface
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Registry{
				lock:            tt.fields.lock,
				customMetrics:   tt.fields.customMetrics,
				externalMetrics: tt.fields.externalMetrics,
			}
			got, err := r.GetExternalMetricClient(tt.args.info)
			if (err != nil) != tt.wantErr {
				t.Errorf("Registry.GetExternalMetricClient() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Registry.GetExternalMetricClient() = %v, want %v", got, tt.want)
			}
		})
	}
}
