// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. Elasticsearch B.V. licenses this file to
// you under the Apache License, Version 2.0 (the "License");
// you may  not use this file except in compliance with the
// License.
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

package elasticsearch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"sync"

	"github.com/elastic/elasticsearch-adapter/pkg/client"
	"github.com/elastic/elasticsearch-adapter/pkg/config"
	"github.com/elastic/elasticsearch-adapter/pkg/tracing"
	esv7 "github.com/elastic/go-elasticsearch/v7"
	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider/helpers"
	"go.elastic.co/apm"
	"go.elastic.co/apm/module/apmelasticsearch"
	apierr "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
	"k8s.io/metrics/pkg/apis/custom_metrics"
	"k8s.io/metrics/pkg/apis/external_metrics"
)

const (
	query = `
{
	"query": {
		"bool": {
			"must": [{
				"exists": {
					"field": "%s"
				}
			}, {
				"match": {
					"kubernetes.namespace": "%s"
				}
			}, {
				"match": {
					"kubernetes.pod.name": "%s"
				}
			}]
		}
	},
	"size": 1,
  "sort": [
    {
      "@timestamp": {
        "order": "desc"
      }
    }
  ]
}
`
)

// MetricsClient is a wrapper around the Elasticsearch client to implement to metrics interface.
type MetricsClient struct {
	*esv7.Client
	metricServerCfg config.MetricServer
	lock            sync.RWMutex

	// metrics list of the metrics currently known ny this client
	metrics map[string]provider.CustomMetricInfo

	// indexedMetrics is used to associate a metric name with an index and a field.
	indexedMetrics map[string]MetricMetadata

	// namer maintains an index of the metric aliases and their real names in the Elasticsearch cluster.
	namer config.Namer

	client dynamic.Interface
	mapper apimeta.RESTMapper

	tracer *apm.Tracer
}

func (mc *MetricsClient) GetConfiguration() config.MetricServer {
	return mc.metricServerCfg
}

func NewElasticsearchClient(
	metricServerCfg config.MetricServer,
	client dynamic.Interface,
	mapper apimeta.RESTMapper,
	tracer *apm.Tracer,
) (*MetricsClient, error) {
	tlsConfig, err := newTLSClientConfig(metricServerCfg.ClientConfig.TLSClientConfig)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	cfg := esv7.Config{
		Addresses: []string{os.ExpandEnv(metricServerCfg.ClientConfig.Host)},
		Transport: apmelasticsearch.WrapRoundTripper(transport),
	}

	if metricServerCfg.ClientConfig.AuthenticationConfig != nil {
		cfg.Username = os.ExpandEnv(metricServerCfg.ClientConfig.AuthenticationConfig.Username)
		cfg.Password = os.ExpandEnv(metricServerCfg.ClientConfig.AuthenticationConfig.Password)
	}

	esClient, err := esv7.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &MetricsClient{
		Client:          esClient,
		metricServerCfg: metricServerCfg,
		client:          client,
		mapper:          mapper,
		tracer:          tracer,
	}, nil
}

var _ client.Interface = &MetricsClient{}

func (mc *MetricsClient) ListCustomMetricInfos() (map[provider.CustomMetricInfo]struct{}, error) {
	if err := mc.discoverMetrics(); err != nil {
		return nil, err
	}
	mc.lock.RLock()
	defer mc.lock.RUnlock()
	customMetrics := make(map[provider.CustomMetricInfo]struct{}, len(mc.metrics))
	for i := range mc.metrics {
		customMetrics[mc.metrics[i]] = struct{}{}
	}
	return customMetrics, nil
}

func (mc *MetricsClient) GetMetricByName(name types.NamespacedName, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValue, error) {
	t, ctx := tracing.NewTransaction(context.TODO(), mc.tracer, "elasticsearch-provider", "GetMetricBySelector")
	defer tracing.EndTransaction(t)
	klog.Infof("elasticsearch.GetMetricByName(name=%v,info=%v,metricSelector=%v)", name, info, metricSelector)
	value, err := mc.valueFor(&ctx, info, name, labels.NewSelector(), []string{}, metricSelector)
	if err != nil {
		return nil, err
	}
	return mc.metricFor(&ctx, value, name, labels.Everything(), info, metricSelector)
}

func (mc *MetricsClient) GetMetricBySelector(namespace string, selector labels.Selector, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValueList, error) {
	t, ctx := tracing.NewTransaction(context.TODO(), mc.tracer, "elasticsearch-provider", "GetMetricBySelector")
	defer tracing.EndTransaction(t)
	klog.Infof("-> elasticsearchProvider.GetMetricBySelector(namespace=%v,selector=%v,info=%v,metricSelector=%v)", namespace, selector, info, metricSelector)
	return mc.metricsFor(&ctx, namespace, selector, info, metricSelector)
}

func (mc *MetricsClient) GetExternalMetric(
	_, _ string,
	_ labels.Selector,
) (*external_metrics.ExternalMetricValueList, error) {
	klog.Error("GetExternalMetric: external are not supported by Elasticsearch metrics client")
	return nil, nil
}

func (mc *MetricsClient) ListExternalMetrics() (map[provider.ExternalMetricInfo]struct{}, error) {
	klog.V(2).Infof("ListAllExternalMetrics: external are not supported by Elasticsearch metrics client")
	return nil, nil
}

func newTLSClientConfig(config *config.TLSClientConfig) (*tls.Config, error) {
	if config == nil {
		// If nothing has been set just return a nil struct
		klog.V(2).Infof("No Elasticsearch TLS configuration provided")
		return nil, nil
	}
	klog.V(2).Infof("Loading Elasticsearch TLS configuration")
	tlsConfig := &tls.Config{
		InsecureSkipVerify: config.Insecure,
	}

	if config.CAFile != "" {
		// Load CA cert
		caCert, err := ioutil.ReadFile(config.CAFile)
		if err != nil {
			return nil, err
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
		tlsConfig.RootCAs = caCertPool
	}
	return tlsConfig, nil
}

// valueFor is a helper function to get just the value of a specific metric
func (mc *MetricsClient) valueFor(
	ctx *context.Context,
	info provider.CustomMetricInfo,
	name types.NamespacedName,
	originalSelector labels.Selector,
	objects []string,
	metricSelector labels.Selector,
) (timestampedMetric, error) {
	defer tracing.Span(ctx)()
	info, _, err := info.Normalized(mc.mapper)
	if err != nil {
		return timestampedMetric{}, err
	}
	mc.lock.RLock()
	defer mc.lock.RUnlock()
	metricName, ok := mc.namer.Get(info.Metric)
	if !ok {
		return timestampedMetric{}, fmt.Errorf("metric name alias for custom metric %s not found", info.Metric)
	}
	info.Metric = metricName
	metadata, ok := mc.indexedMetrics[info.Metric]
	if !ok {
		return timestampedMetric{}, fmt.Errorf("no metadata for metric %s", info.Metric)
	}
	value, err := getMetricForPod(ctx, mc.Client, metadata, name, info, metricSelector, originalSelector, objects)
	if err != nil {
		return timestampedMetric{}, err
	}

	// TODO: handle metricSelector
	/*if !metricSelector.Matches(value.labels) {
		return resource.Quantity{}, provider.NewMetricNotFoundForSelectorError(info.GroupResource, info.Metric, name.Name, metricSelector)
	}*/

	return value, nil

}

// metricFor is a helper function which formats a value, metric, and object info into a MetricValue which can be returned by the metrics API
func (mc *MetricsClient) metricFor(
	ctx *context.Context,
	timeStampedMetric timestampedMetric,
	name types.NamespacedName,
	selector labels.Selector,
	info provider.CustomMetricInfo,
	metricSelector labels.Selector,
) (*custom_metrics.MetricValue, error) {
	defer tracing.Span(ctx)()
	objRef, err := helpers.ReferenceFor(mc.mapper, name, info)
	if err != nil {
		return nil, err
	}

	metric := &custom_metrics.MetricValue{
		DescribedObject: objRef,
		Metric: custom_metrics.MetricIdentifier{
			Name: info.Metric,
		},
		Timestamp: timeStampedMetric.Timestamp,
		Value:     timeStampedMetric.Value,
	}

	if len(metricSelector.String()) > 0 {
		sel, err := metav1.ParseToLabelSelector(metricSelector.String())
		if err != nil {
			return nil, err
		}
		metric.Metric.Selector = sel
	}

	return metric, nil
}

// metricsFor is a wrapper used by GetMetricBySelector to format several metrics which match a resource selector
func (mc *MetricsClient) metricsFor(
	ctx *context.Context,
	namespace string,
	selector labels.Selector,
	info provider.CustomMetricInfo,
	metricSelector labels.Selector,
) (*custom_metrics.MetricValueList, error) {
	defer tracing.Span(ctx)()
	klog.Infof(fmt.Sprintf("metricsFor(%s,%s)", selector, info))
	names, err := helpers.ListObjectNames(mc.mapper, mc.client, namespace, selector, info)
	if err != nil {
		return nil, err
	}

	res := make([]custom_metrics.MetricValue, 0, len(names))
	for _, name := range names {
		namespacedName := types.NamespacedName{Name: name, Namespace: namespace}
		value, err := mc.valueFor(ctx, info, namespacedName, selector, names, metricSelector)
		if err != nil {
			if apierr.IsNotFound(err) {
				continue
			}
			return nil, err
		}

		metric, err := mc.metricFor(ctx, value, namespacedName, selector, info, metricSelector)
		if err != nil {
			return nil, err
		}
		res = append(res, *metric)
	}

	return &custom_metrics.MetricValueList{
		Items: res,
	}, nil
}
