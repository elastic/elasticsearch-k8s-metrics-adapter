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

package client

import (
	"github.com/elastic/elasticsearch-adapter/pkg/config"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/metrics/pkg/apis/custom_metrics"
	"k8s.io/metrics/pkg/apis/external_metrics"
	"sigs.k8s.io/custom-metrics-apiserver/pkg/provider"
)

type Interface interface {
	GetConfiguration() config.MetricServer

	ListCustomMetricInfos() (map[provider.CustomMetricInfo]struct{}, error)
	GetMetricByName(name types.NamespacedName, info provider.CustomMetricInfo, selector labels.Selector) (*custom_metrics.MetricValue, error)
	GetMetricBySelector(namespace string, selector labels.Selector, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValueList, error)

	ListExternalMetrics() (map[provider.ExternalMetricInfo]struct{}, error)
	GetExternalMetric(name, namespace string, selector labels.Selector) (*external_metrics.ExternalMetricValueList, error)
}
