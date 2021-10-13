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

package scheduler

import (
	"sync"
	"time"

	"github.com/elastic/elasticsearch-adapter/pkg/client"
	"github.com/elastic/elasticsearch-adapter/pkg/config"
	"k8s.io/klog/v2"
	"sigs.k8s.io/custom-metrics-apiserver/pkg/provider"
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
		c:  c,
		wg: wg,
	}
}

type metricJob struct {
	c              client.Interface
	wg             *sync.WaitGroup
	syncDone       sync.Once
	listeners      []MetricListener
	errorListeners []ErrorListener

	previousCustomMetrics   map[provider.CustomMetricInfo]struct{}
	previousExternalMetrics map[provider.ExternalMetricInfo]struct{}
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
			klog.Errorf(
				"Failed to update custom metric list from  %s / %s : %v",
				m.GetClient().GetConfiguration().Name,
				m.GetClient().GetConfiguration().ClientConfig.Host,
				err,
			)
			m.publishError(config.CustomMetricType, err)
			return
		}

		klog.V(1).Infof(
			"%d custom metrics from %s / %s",
			len(customMetrics),
			m.GetClient().GetConfiguration().Name,
			m.GetClient().GetConfiguration().ClientConfig.Host,
		)

		for _, listener := range m.listeners {
			listener.UpdateCustomMetrics(m.c, customMetrics)
		}
	}

	if m.GetClient().GetConfiguration().MetricTypes.HasType(config.ExternalMetricType) {
		externalMetrics, err := m.c.ListExternalMetrics()
		if err != nil {
			klog.Errorf(
				"Failed to update external metric list from  %s / %s : %v",
				m.GetClient().GetConfiguration().Name,
				m.GetClient().GetConfiguration().ClientConfig.Host,
				err,
			)
			m.publishError(config.ExternalMetricType, err)
			return
		}

		klog.V(1).Infof(
			"%d external metrics from %s / %s",
			len(externalMetrics),
			m.GetClient().GetConfiguration().Name,
			m.GetClient().GetConfiguration().ClientConfig.Host,
		)

		for _, listener := range m.listeners {
			listener.UpdateExternalMetrics(m.c, externalMetrics)
		}
	}

	m.syncDone.Do(func() {
		klog.V(1).Infof(
			"First sync successful from %s / %s",
			m.GetClient().GetConfiguration().Name,
			m.GetClient().GetConfiguration().ClientConfig.Host,
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
