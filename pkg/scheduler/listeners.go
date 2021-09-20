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
	"github.com/elastic/elasticsearch-adapter/pkg/client"
	"github.com/elastic/elasticsearch-adapter/pkg/config"
	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
)

type MetricListener interface {
	UpdateCustomMetrics(client.Interface, map[provider.CustomMetricInfo]struct{})
	UpdateExternalMetrics(client.Interface, map[provider.ExternalMetricInfo]struct{})
}

type ErrorListener interface {
	OnError(c client.Interface, metricType config.MetricType, err error)
}
