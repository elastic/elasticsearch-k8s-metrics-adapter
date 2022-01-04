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

package monitoring

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/elastic/elasticsearch-adapter/pkg/client"
	"github.com/elastic/elasticsearch-adapter/pkg/config"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/metrics/pkg/apis/custom_metrics"
	"k8s.io/metrics/pkg/apis/external_metrics"
	"sigs.k8s.io/custom-metrics-apiserver/pkg/provider"
)

func TestServer_ServeHTTP(t *testing.T) {

	server := NewServer(&config.Config{
		ReadinessProbe: config.ReadinessProbe{},
		MetricServers: []config.MetricServer{
			{
				Name: "metric_server1",
			},
			{
				Name:        "metric_server2",
				MetricTypes: &config.MetricTypes{config.CustomMetricType},
			},
			{
				Name:        "metric_server3",
				MetricTypes: &config.MetricTypes{config.ExternalMetricType},
			},
		},
	}, 0, false)

	// Initial status: not ready
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, nil)
	assert.Equal(t, 503, recorder.Code)

	// server1 provides some metrics
	server.UpdateCustomMetrics(newFakeClient("metric_server1"), nil)
	server.UpdateExternalMetrics(newFakeClient("metric_server1"), nil)
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, nil)
	assert.Equal(t, 503, recorder.Code) // 503 as we are still waiting for others servers

	// server2 provides some custom metrics
	server.UpdateCustomMetrics(newFakeClient("metric_server2"), nil)
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, nil)
	assert.Equal(t, 503, recorder.Code) // 503 as we are still waiting for server3

	// server3 provides some external metrics
	server.UpdateExternalMetrics(newFakeClient("metric_server3"), nil)
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, nil)
	assert.Equal(t, 200, recorder.Code)

	// server2 is having some issues
	server.OnError(newFakeClient("metric_server2"), config.CustomMetricType, errors.New("foo"))
	server.OnError(newFakeClient("metric_server2"), config.CustomMetricType, errors.New("foo"))
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, nil)
	assert.Equal(t, 200, recorder.Code) // still waiting for 1 error until 503

	// server2 is having some issues
	server.OnError(newFakeClient("metric_server2"), config.CustomMetricType, errors.New("foo"))
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, nil)
	assert.Equal(t, 503, recorder.Code) // error threshold reached

	// server2 has recovered
	server.UpdateCustomMetrics(newFakeClient("metric_server2"), nil)
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, nil)
	assert.Equal(t, 200, recorder.Code)

}

func newFakeClient(name string) client.Interface {
	return &fakeClient{name: name}
}

type fakeClient struct {
	name string
}

func (f fakeClient) GetConfiguration() config.MetricServer {
	return config.MetricServer{
		Name: f.name,
	}
}

func (f fakeClient) ListCustomMetricInfos() (map[provider.CustomMetricInfo]struct{}, error) {
	panic("implement me")
}

func (f fakeClient) GetMetricByName(name types.NamespacedName, info provider.CustomMetricInfo, selector labels.Selector) (*custom_metrics.MetricValue, error) {
	panic("implement me")
}

func (f fakeClient) GetMetricBySelector(namespace string, selector labels.Selector, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValueList, error) {
	panic("implement me")
}

func (f fakeClient) ListExternalMetrics() (map[provider.ExternalMetricInfo]struct{}, error) {
	panic("implement me")
}

func (f fakeClient) GetExternalMetric(name, namespace string, selector labels.Selector) (*external_metrics.ExternalMetricValueList, error) {
	panic("implement me")
}

var _ client.Interface = &fakeClient{}
