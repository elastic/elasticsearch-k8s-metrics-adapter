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

package registry

import (
	"fmt"
	"sort"

	"github.com/elastic/elasticsearch-adapter/pkg/client"
)

type metricClients []client.Interface

func newMetricClients() *metricClients {
	clients := make(metricClients, 0)
	return &clients
}

func (c metricClients) Len() int {
	return len(c)
}

func (c metricClients) Less(i, j int) bool {
	// We want client with higher priority to be at the beginning of the array
	return c[i].GetConfiguration().Priority > c[j].GetConfiguration().Priority
}

func (c metricClients) Swap(i, j int) {
	c[i], c[j] = c[j], c[i]
}

func (c *metricClients) addOrUpdateClient(metricClient client.Interface) {
	found := -1
	for i, s := range *c {
		if s.GetConfiguration().Name == metricClient.GetConfiguration().Name {
			found = i
			break
		}
	}
	if found != -1 {
		(*c)[found] = metricClient
	} else {
		*c = append(*c, metricClient)
	}
	sort.Sort(c)
}

func (c *metricClients) removeClient(sourceName string) (empty bool) {
	found := -1
	for i, s := range *c {
		if s.GetConfiguration().Name == sourceName {
			found = i
			break
		}
	}
	if found != -1 {
		*c = append((*c)[:found], (*c)[found+1:]...)
		sort.Sort(c)
	}
	return c.Len() == 0
}

func (c *metricClients) getBestMetricClient() (client.Interface, error) {
	if c.Len() == 0 {
		return nil, fmt.Errorf("no metric backend for metric")
	}
	service := (*c)[0]
	return service, nil
}
