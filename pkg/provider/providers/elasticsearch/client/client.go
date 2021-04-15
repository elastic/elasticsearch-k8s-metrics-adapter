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
	"fmt"
	"net/http"
	"os"

	esv7 "github.com/elastic/go-elasticsearch/v7"
	"k8s.io/apimachinery/pkg/types"
)

const (
	vElasticsearchURL      = "ELASTICSEARCH_URL"
	vElasticsearchUsername = "ELASTICSEARCH_USERNAME"
	vElasticsearchPassword = "ELASTICSEARCH_PASSWORD"

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

func NewElasticsearchClient() (*esv7.Client, error) {
	elasticsearchURL := os.Getenv(vElasticsearchURL)
	if len(elasticsearchURL) == 0 {
		return nil, mandatoryEnvError(vElasticsearchURL)
	}
	elasticsearchUsername := os.Getenv(vElasticsearchUsername)
	if len(elasticsearchUsername) == 0 {
		return nil, mandatoryEnvError(vElasticsearchUsername)
	}
	elasticsearchPassword := os.Getenv(vElasticsearchPassword)
	if len(elasticsearchPassword) == 0 {
		return nil, mandatoryEnvError(vElasticsearchPassword)
	}

	cfg := esv7.Config{
		Username:  elasticsearchUsername,
		Password:  elasticsearchPassword,
		Addresses: []string{elasticsearchURL},
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	return esv7.NewClient(cfg)
}

func mandatoryEnvError(name string) error {
	return fmt.Errorf("environment variable %s must be set", name)
}
