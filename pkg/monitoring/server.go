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
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/klog/v2"
	"sigs.k8s.io/custom-metrics-apiserver/pkg/provider"

	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/client"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/config"
	"go.elastic.co/apm"
	"go.elastic.co/apm/module/apmprometheus"
)

const defaultFailureThreshold = 3

var (
	clientErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "client_errors_total",
		Help: "The total number of errors raised by a client",
	}, []string{"client", "type"})
	clientSuccess = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "client_success_total",
		Help: "The total number of successful call to a metrics server",
	}, []string{"client", "type"})
	metrics = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "metrics_count",
		Help: "The current number of metrics served by this metrics server",
	}, []string{"client", "type"})
)

type Counters struct {
	CustomMetrics   map[string]int `json:"customMetrics,omitempty"`
	ExternalMetrics map[string]int `json:"externalMetrics,omitempty"`
}

func NewCounters() *Counters {
	return &Counters{
		CustomMetrics:   make(map[string]int),
		ExternalMetrics: make(map[string]int),
	}
}

func NewServer(adapterCfg *config.Config, port int, enablePrometheusMetrics bool, tracer *apm.Tracer) *Server {
	failureThreshold := adapterCfg.ReadinessProbe.FailureThreshold
	if failureThreshold == 0 {
		failureThreshold = defaultFailureThreshold
	}
	clientSuccesses := NewCounters()
	for _, clientCfg := range adapterCfg.MetricServers {
		if clientCfg.MetricTypes.HasType(config.CustomMetricType) {
			clientSuccesses.CustomMetrics[clientCfg.Name] = 0
		}
		if clientCfg.MetricTypes.HasType(config.ExternalMetricType) {
			clientSuccesses.ExternalMetrics[clientCfg.Name] = 0
		}
	}
	// Register the default prometheus
	tracer.RegisterMetricsGatherer(apmprometheus.Wrap(prometheus.DefaultGatherer))
	return &Server{
		lock:                    sync.RWMutex{},
		adapterCfg:              adapterCfg,
		monitoringPort:          port,
		enablePrometheusMetrics: enablePrometheusMetrics,
		clientFailures:          NewCounters(),
		clientSuccesses:         clientSuccesses,
		failureThreshold:        failureThreshold,
	}
}

type Server struct {
	lock                    sync.RWMutex
	adapterCfg              *config.Config
	monitoringPort          int
	failureThreshold        int
	enablePrometheusMetrics bool
	clientFailures          *Counters
	clientSuccesses         *Counters
}

func (m *Server) OnError(c client.Interface, metricType config.MetricType, err error) {
	clientName := c.GetConfiguration().Name
	m.lock.Lock()
	defer m.lock.Unlock()
	if metricType == config.CustomMetricType {
		m.clientFailures.CustomMetrics[clientName]++
	}
	if metricType == config.ExternalMetricType {
		m.clientFailures.ExternalMetrics[clientName]++
	}
	clientErrors.WithLabelValues(c.GetConfiguration().Name, string(metricType)).Inc()
}

func (m *Server) UpdateExternalMetrics(c client.Interface, ems map[provider.ExternalMetricInfo]struct{}) {
	clientName := c.GetConfiguration().Name
	m.lock.Lock()
	defer m.lock.Unlock()
	// reset client failures as we got some metrics
	m.clientFailures.ExternalMetrics[clientName] = 0
	// increment success counters
	m.clientSuccesses.ExternalMetrics[clientName]++
	clientSuccess.WithLabelValues(c.GetConfiguration().Name, string(config.ExternalMetricType)).Inc()
	// update external metrics stats
	metrics.WithLabelValues(c.GetConfiguration().Name, string(config.ExternalMetricType)).Set(float64(len(ems)))
}

func (m *Server) UpdateCustomMetrics(c client.Interface, cms map[provider.CustomMetricInfo]struct{}) {
	clientName := c.GetConfiguration().Name
	m.lock.Lock()
	defer m.lock.Unlock()
	// reset client failures as we got some metrics
	m.clientFailures.CustomMetrics[clientName] = 0
	// increment success counters
	m.clientSuccesses.CustomMetrics[clientName]++
	clientSuccess.WithLabelValues(c.GetConfiguration().Name, string(config.CustomMetricType)).Inc()
	// update custom metrics stats
	metrics.WithLabelValues(c.GetConfiguration().Name, string(config.CustomMetricType)).Set(float64(len(cms)))
}

func (m *Server) Start() {
	http.Handle("/metrics", promhttp.Handler())
	http.Handle("/readyz", m)
	_ = http.ListenAndServe(fmt.Sprintf(":%d", m.monitoringPort), nil)
}

func (m *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	status := http.StatusOK
	m.lock.RLock()
	defer m.lock.RUnlock()
	for _, server := range m.adapterCfg.MetricServers {
		if customMetricsSuccess, hasCustomMetrics := m.clientSuccesses.CustomMetrics[server.Name]; hasCustomMetrics && customMetricsSuccess == 0 {
			status = http.StatusServiceUnavailable
			klog.Errorf("client %s has not retrieved an initial set of custom metrics yet", server.Name)
			break
		}

		if externalMetricsSuccess, hasExternalMetrics := m.clientSuccesses.ExternalMetrics[server.Name]; hasExternalMetrics && externalMetricsSuccess == 0 {
			status = http.StatusServiceUnavailable
			klog.Errorf("client %s has not retrieved an initial set of external metrics yet", server.Name)
			break
		}

		if m.clientFailures.CustomMetrics[server.Name] >= m.failureThreshold {
			status = http.StatusServiceUnavailable
			klog.Errorf("client %s got %d consecutive failures while retrieving custom metrics", server.Name, m.clientFailures.CustomMetrics[server.Name])
			break
		}

		if m.clientFailures.ExternalMetrics[server.Name] >= m.failureThreshold {
			status = http.StatusServiceUnavailable
			klog.Errorf("client %s got %d consecutive failures while retrieving external metrics", server.Name, m.clientFailures.ExternalMetrics[server.Name])
			break
		}
	}
	err := writeJSONResponse(writer, status, ClientsHealthResponse{ClientFailures: m.clientFailures, ClientOk: m.clientSuccesses})
	if err != nil {
		klog.Errorf("Failed to write monitoring JSON response: %v", err)
	}
}

type ClientsHealthResponse struct {
	ClientFailures *Counters `json:"consecutiveFailures,omitempty"`
	ClientOk       *Counters `json:"successTotal,omitempty"`
}

func writeJSONResponse(w http.ResponseWriter, code int, resp interface{}) error {
	enc, err := json.MarshalIndent(resp, "", "\t")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	_, err = w.Write(enc)
	if err != nil {
		return err
	}
	return nil
}
