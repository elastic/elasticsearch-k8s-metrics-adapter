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

package scheduler

import (
	"sync"
	"time"

	"github.com/go-logr/logr"

	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/client"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/config"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/log"
)

type Job interface {
	start()
	GetClient() client.Interface
	WithMetricListeners(listeners ...MetricListener) Job
	WithErrorListeners(listeners ...ErrorListener) Job
}

var _ Job = &metricJob{}

func newMetricJob(c client.Interface, wg *sync.WaitGroup) Job {
	return &metricJob{
		logger: log.ForPackage("job"),
		c:      c,
		wg:     wg,
	}
}

type metricJob struct {
	logger         logr.Logger
	c              client.Interface
	wg             *sync.WaitGroup
	syncDone       sync.Once
	listeners      []MetricListener
	errorListeners []ErrorListener
}

func (m *metricJob) start() {
	go func() {
		// Attempt to get a first set of metrics
		m.refreshMetrics()
		dateTicker := time.NewTicker(1 * time.Minute)
		for range dateTicker.C {
			m.refreshMetrics()
		}
	}()
}

func (m *metricJob) refreshMetrics() {
	if m.GetClient().GetConfiguration().MetricTypes.HasType(config.CustomMetricType) {
		customMetrics, err := m.c.ListCustomMetricInfos()
		if err != nil {
			m.logger.Error(err,
				"Failed to update custom metric list",
				"client_name", m.GetClient().GetConfiguration().Name,
				"client_host", m.GetClient().GetConfiguration().ClientConfig.Host,
			)
			m.publishError(config.CustomMetricType, err)
			return
		}

		m.logger.V(1).Info(
			"Refreshed custom metrics",
			"count", len(customMetrics),
			"client_name", m.GetClient().GetConfiguration().Name,
			"client_host", m.GetClient().GetConfiguration().ClientConfig.Host,
		)

		for _, listener := range m.listeners {
			listener.UpdateCustomMetrics(m.c, customMetrics)
		}
	}

	if m.GetClient().GetConfiguration().MetricTypes.HasType(config.ExternalMetricType) {
		externalMetrics, err := m.c.ListExternalMetrics()
		if err != nil {
			m.logger.Error(err,
				"Failed to update external metric list",
				"client_name", m.GetClient().GetConfiguration().Name,
				"client_host", m.GetClient().GetConfiguration().ClientConfig.Host,
			)
			m.publishError(config.ExternalMetricType, err)
			return
		}

		m.logger.V(1).Info(
			"Refreshed external metrics",
			"metrics_count", len(externalMetrics),
			"client_name", m.GetClient().GetConfiguration().Name,
			"client_host", m.GetClient().GetConfiguration().ClientConfig.Host,
		)

		for _, listener := range m.listeners {
			listener.UpdateExternalMetrics(m.c, externalMetrics)
		}
	}

	m.syncDone.Do(func() {
		m.logger.V(1).Info(
			"First sync successful",
			"client_name", m.GetClient().GetConfiguration().Name,
			"client_host", m.GetClient().GetConfiguration().ClientConfig.Host,
		)
		m.wg.Done()
	})
}

func (m *metricJob) publishError(metricType config.MetricType, err error) {
	for _, listener := range m.errorListeners {
		listener.OnError(m.c, metricType, err)
	}
}

func (m *metricJob) GetClient() client.Interface {
	return m.c
}

func (m *metricJob) WithMetricListeners(listeners ...MetricListener) Job {
	m.listeners = append(m.listeners, listeners...)
	return m
}

func (m *metricJob) WithErrorListeners(listeners ...ErrorListener) Job {
	m.errorListeners = append(m.errorListeners, listeners...)
	return m
}
