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

package monitoring

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/elastic/elasticsearch-adapter/pkg/client"
	"github.com/elastic/elasticsearch-adapter/pkg/config"
	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/klog/v2"
)

const defaultFailureThreshold = 3

var (
	clientErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "client_errors_total",
		Help: "The total number of errors raised by a client",
	}, []string{"client"})
	clientSuccess = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "client_success_total",
		Help: "The total number of successful call to a metrics server",
	}, []string{"client"})
	customMetrics = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "custom_metrics",
		Help: "The current number of custom metrics served by this metrics server",
	}, []string{"client"})
	externalMetrics = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "external_metrics",
		Help: "The current number of external metrics served by this metrics server",
	}, []string{"client"})
)

type Counters map[string]int

func (m *Server) ClientFailures() Counters {
	m.lock.RLock()
	defer m.lock.RUnlock()
	c := make(map[string]int, len(m.clientFailures))
	for k, v := range m.clientFailures {
		c[k] = v
	}
	return c
}

func NewServer(config *config.Config, port int, enablePrometheusMetrics bool) *Server {
	failureThreshold := config.ReadinessProbe.FailureThreshold
	if failureThreshold == 0 {
		failureThreshold = defaultFailureThreshold
	}
	clientFailures := make(map[string]int)
	for _, clientCfg := range config.MetricServers {
		clientFailures[clientCfg.Name] = -1
	}
	return &Server{
		lock:                    sync.RWMutex{},
		monitoringPort:          port,
		enablePrometheusMetrics: enablePrometheusMetrics,
		clientFailures:          clientFailures,
		failureThreshold:        failureThreshold,
	}
}

type Server struct {
	lock                    sync.RWMutex
	monitoringPort          int
	failureThreshold        int
	enablePrometheusMetrics bool
	clientFailures          Counters
}

func (m *Server) OnError(c client.Interface, err error) {
	clientName := c.GetConfiguration().Name
	m.lock.Lock()
	defer m.lock.Unlock()
	m.clientFailures[clientName]++
	clientErrors.WithLabelValues(c.GetConfiguration().Name).Inc()
}

func (m *Server) UpdateMetrics(c client.Interface, cms map[provider.CustomMetricInfo]struct{}, ems map[provider.ExternalMetricInfo]struct{}) {
	clientName := c.GetConfiguration().Name
	m.lock.Lock()
	defer m.lock.Unlock()
	// reset client failures as we got some metrics
	m.clientFailures[clientName] = 0
	clientSuccess.WithLabelValues(c.GetConfiguration().Name).Inc()
	// update metrics stats
	customMetrics.WithLabelValues(c.GetConfiguration().Name).Set(float64(len(cms)))
	externalMetrics.WithLabelValues(c.GetConfiguration().Name).Set(float64(len(ems)))
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
	for c, failures := range m.clientFailures {
		if failures == -1 {
			status = http.StatusServiceUnavailable
			klog.Errorf("client %s has not retrieved an initial set of metrics yet", c)
			break
		}
		if failures >= m.failureThreshold {
			status = http.StatusServiceUnavailable
			klog.Errorf("client %s got %d consecutive failures", c, failures)
			break
		}
	}
	err := writeJSONResponse(writer, status, ClientsHealthResponse{ClientFailures: m.clientFailures})
	if err != nil {
		klog.Errorf("Failed to write monitoring JSON response: %v", err)
	}
}

type ClientsHealthResponse struct {
	ClientFailures map[string]int `json:"clientFailures,omitempty"`
}

func writeJSONResponse(w http.ResponseWriter, code int, resp interface{}) error {
	enc, err := json.Marshal(resp)
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
