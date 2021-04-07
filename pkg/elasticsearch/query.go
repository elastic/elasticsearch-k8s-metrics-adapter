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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/elastic/elasticsearch-adapter/pkg/config"
	esclient "github.com/elastic/elasticsearch-adapter/pkg/elasticsearch/client"
	esv7 "github.com/elastic/go-elasticsearch/v7"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

func getMetricForPod(
	esClient *esv7.Client,
	metadata config.MetricMetadata,
	name types.NamespacedName,
	metric string,
	originalSelector labels.Selector,
	objects []string,
) (timestampedMetric, error) {

	var query string
	if metadata.Search != nil {
		// User specified a custom query
		podSelectors := make(map[string]string)
		requirements, _ := originalSelector.Requirements()
		for _, requirement := range requirements {
			values := requirement.Values()
			if len(values) == 0 {
				continue
			}
			// Get first item in the selector
			for selectorValue := range values {
				podSelectors[requirement.Key()] = selectorValue
			}
		}

		tplBuffer := bytes.Buffer{}

		if err := metadata.Search.Template.Execute(&tplBuffer, esclient.CustomQueryParams{
			Metric:       metric,
			Pod:          name.Name,
			PodSelectors: podSelectors,
			Namespace:    name.Namespace,
			Objects:      objects,
		}); err != nil {
			return timestampedMetric{}, err
		}

		query = tplBuffer.String()
	} else {
		query = esclient.QueryFor(esclient.QueryParams{
			Metric: metric,
			Name:   name,
		})
	}

	res, err := esClient.Search(
		esClient.Search.WithContext(context.Background()),
		esClient.Search.WithIndex(metadata.Indices...),
		esClient.Search.WithBody(strings.NewReader(query)),
		esClient.Search.WithTrackTotalHits(true),
		esClient.Search.WithPretty(),
	)
	if err != nil {
		return timestampedMetric{}, err
	}
	defer res.Body.Close()

	if res.IsError() {
		var e map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&e); err != nil {
			return timestampedMetric{}, fmt.Errorf("error parsing the response body: %s", err)
		} else {
			// Print the response status and error information.
			return timestampedMetric{}, fmt.Errorf("[%s] %s: %s",
				res.Status(),
				e["error"].(map[string]interface{})["type"],
				e["error"].(map[string]interface{})["reason"],
			)
		}
	}

	var r map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		log.Fatalf("Error parsing the response body: %s", err)
	}

	var value float64
	var timestamp metav1.Time

	if metadata.Search != nil {
		iter := metadata.Search.MetricResultQuery.Run(r)
		for {
			v, ok := iter.Next()
			if !ok {
				break
			}
			if err, ok := v.(error); ok {
				return timestampedMetric{}, err
			}
			if value, err = getFloat(v); err != nil {
				return timestampedMetric{}, err
			}
		}
		iter = metadata.Search.TimestampResultQuery.Run(r)
		for {
			v, ok := iter.Next()
			if !ok {
				break
			}
			if err, ok := v.(error); ok {
				return timestampedMetric{}, err
			}
			if timestamp, err = getTimestamp(v); err != nil {
				return timestampedMetric{}, err
			}
		}
	} else {
		// Get the result from the document.
		metricDocument, err := getMetricDocument(r)
		if err != nil {
			return timestampedMetric{}, err
		}

		if value, err = getMetricValue("_source."+metric, metricDocument); err != nil {
			return timestampedMetric{}, err
		}

		if timestamp, err = getTimestampFromDocument("_source.@timestamp", metricDocument); err != nil {
			return timestampedMetric{}, err
		}
	}

	var q *resource.Quantity
	if math.IsNaN(value) {
		q = resource.NewQuantity(0, resource.DecimalSI)
	} else {
		q = resource.NewMilliQuantity(int64(value*1000.0), resource.DecimalSI)
	}

	return timestampedMetric{
		value:     *q,
		timestamp: timestamp,
	}, nil
}

func getFloat(v interface{}) (float64, error) {
	switch i := v.(type) {
	case float64:
		return i, nil
	case float32:
		return float64(i), nil
	case int64:
		return float64(i), nil
	default:
		return math.NaN(), errors.New("getFloat: value is of incompatible type")
	}
}

func getValue(path string, doc map[string]interface{}) (interface{}, error) {
	segments := strings.Split(path, ".")
	if !(len(segments) > 0) {
		return 0, fmt.Errorf("no segment in path")
	}
	isLeaf := len(segments) == 1

	root, segments := segments[0], segments[1:]
	rootDoc, exists := doc[root]
	if !exists {
		keys := make([]string, 0, len(doc))
		for k := range doc {
			keys = append(keys, k)
		}
		return 0, fmt.Errorf("can't find leaf %s in [%s]", root, strings.Join(keys, ","))
	}

	if isLeaf {
		// Value is expected
		return rootDoc, nil
	}
	if innerDoc, ok := rootDoc.(map[string]interface{}); ok {
		return getValue(strings.Join(segments, "."), innerDoc)
	}
	return 0, fmt.Errorf("not a document: %v", rootDoc)
}

func getTimestampFromDocument(path string, doc map[string]interface{}) (metav1.Time, error) {
	v, err := getValue(path, doc)
	if err != nil {
		return metav1.Unix(0, 0), err
	}

	return getTimestamp(v)

}

func getTimestamp(v interface{}) (metav1.Time, error) {
	if t, ok := v.(string); ok {
		t, err := time.Parse(time.RFC3339, t)
		if err != nil {
			return metav1.Unix(0, 0), err
		}
		return metav1.NewTime(t), nil
	}

	return metav1.Unix(0, 0), fmt.Errorf("not a string: %v", v)
}

func getMetricValue(path string, doc map[string]interface{}) (float64, error) {
	raw, err := getValue(path, doc)
	if err != nil {
		return 0, err
	}

	switch v := raw.(type) {
	case int:
		return float64(v), nil
	case float64:
		return v, nil
	default:
		return 0, fmt.Errorf("NaN: %v", v)
	}

}

func getMetricDocument(doc map[string]interface{}) (map[string]interface{}, error) {
	metaHits, hasMetaHits := doc["hits"]
	if !hasMetaHits {
		return nil, fmt.Errorf("no hits in %v", metaHits)
	}

	var documents interface{}
	if hits, ok := metaHits.(map[string]interface{}); ok {
		documents, _ = hits["hits"]
	} else {
		return nil, fmt.Errorf("cannot convert hits: %v", metaHits)
	}

	if documents, ok := documents.([]interface{}); ok {
		if len(documents) == 0 {
			return nil, fmt.Errorf("no documents in %v", doc)
		}
		document := documents[0]
		if result, ok := document.(map[string]interface{}); ok {
			return result, nil
		}
	}

	return nil, fmt.Errorf("no hits in %v", metaHits)

}
