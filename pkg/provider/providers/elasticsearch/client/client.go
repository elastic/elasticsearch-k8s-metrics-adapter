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

package client

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"

	"github.com/elastic/elasticsearch-adapter/pkg/config"
	esv7 "github.com/elastic/go-elasticsearch/v7"
	"go.elastic.co/apm/module/apmelasticsearch"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
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

type QueryParams struct {
	Metric string
	Name   types.NamespacedName
}

type CustomQueryParams struct {
	Metric       string
	Pod          string
	PodSelectors map[string]string
	Namespace    string
	// All the objects in the context of the metric query, for example other Pods for the deployments
	Objects []string
}

func QueryFor(params QueryParams) string {
	return fmt.Sprintf(query, params.Metric, params.Name.Namespace, params.Name.Name)
}

func NewElasticsearchClient(config *config.HTTPClientConfig) (*esv7.Client, error) {
	tlsConfig, err := newTLSClientConfig(config.TLSClientConfig)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	cfg := esv7.Config{
		Username:  os.ExpandEnv(config.AuthenticationConfig.Username),
		Password:  os.ExpandEnv(config.AuthenticationConfig.Password),
		Addresses: []string{os.ExpandEnv(config.Host)},
		Transport: apmelasticsearch.WrapRoundTripper(transport),
	}

	return esv7.NewClient(cfg)
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
