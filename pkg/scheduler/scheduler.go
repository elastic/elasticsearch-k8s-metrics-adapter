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

	"github.com/elastic/elasticsearch-adapter/pkg/client"
	"k8s.io/klog/v2"
)

type Scheduler struct {
	wg      *sync.WaitGroup
	sources []Job
}

// Start starts all the metric sources.
func (s *Scheduler) Start() *Scheduler {
	for _, source := range s.sources {
		source.start()
	}
	return s
}

func (s *Scheduler) WithMetricListeners(listeners ...MetricListener) *Scheduler {
	for i := range s.sources {
		for j := range listeners {
			s.sources[i].WithMetricListeners(listeners[j])
		}
	}
	return s
}

func (s *Scheduler) WithErrorListeners(listeners ...ErrorListener) *Scheduler {
	for i := range s.sources {
		for j := range listeners {
			s.sources[i].WithErrorListeners(listeners[j])
		}
	}
	return s
}

// NewScheduler creates a new scheduler with an initial set of clients.
func NewScheduler(clients ...client.Interface) *Scheduler {
	scheduler := &Scheduler{
		wg:      &sync.WaitGroup{},
		sources: make([]Job, len(clients)),
	}
	for i := range clients {
		scheduler.sources[i] = newMetricJob(clients[i], scheduler.wg)
	}
	scheduler.wg.Add(len(clients))
	return scheduler
}

// WithClients adds more metrics clients to the scheduler.
func (s *Scheduler) WithClients(clients ...client.Interface) *Scheduler {
	for i := range clients {
		source := newMetricJob(clients[i], s.wg)
		s.sources = append(s.sources, source)

	}
	s.wg.Add(len(clients))
	return s
}

// WaitInitialSync blocks until all the
func (s *Scheduler) WaitInitialSync() *Scheduler {
	klog.Infof("Wait until an initial metric list is grabbed from %d metric clients", len(s.sources))
	s.wg.Wait()
	klog.Infof("Initial metric list is grabbed from %d metric clients", len(s.sources))
	return s
}
