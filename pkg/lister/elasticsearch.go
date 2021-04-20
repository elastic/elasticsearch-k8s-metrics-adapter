package lister

import (
	"context"
	"encoding/json"
	"fmt"
	"text/template"

	"github.com/elastic/elasticsearch-adapter/pkg/common"
	"github.com/elastic/elasticsearch-adapter/pkg/config"
	esv7 "github.com/elastic/go-elasticsearch/v7"
	"github.com/elastic/go-elasticsearch/v7/esapi"
	"github.com/itchyny/gojq"
	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
)

func (ml *MetricLister) elasticsearchMetrics(
	esProvider provider.MetricsProvider,
) ([]provider.CustomMetricInfo, map[string]common.MetricMetadata, error) {
	esClient := ml.esClient
	metricSets := ml.cfg.MetricSets
	metricRecoder := newRecorder(esProvider)

	// We first record static fields, they do not require to read the mapping
	for _, metricSet := range metricSets {
		for _, field := range metricSet.Fields {
			if len(field.Name) > 0 {
				search := field.Search
				search.Template = template.Must(template.New("").Parse(search.Body))
				metricResultQuery, err := gojq.Parse(search.MetricPath)
				if err != nil {
					klog.Fatalf("Error while parsing metricResultQuery for field %s: error: %v", field.Name, err)

				}
				search.MetricResultQuery = metricResultQuery
				timestampResultQuery, err := gojq.Parse(search.TimestampPath)
				if err != nil {
					klog.Fatalf("Error while parsing timestampResultQuery for field %s: error: %v", field.Name, err)

				}
				search.TimestampResultQuery = timestampResultQuery
				// This is a static field, save the request body and the metric path
				metricRecoder.indexedMetrics[field.Name] = common.MetricMetadata{
					MetricsProvider: esProvider,
					Search:          &search,
					Indices:         metricSet.Indices,
				}
				metricRecoder.metrics = append(metricRecoder.metrics, provider.CustomMetricInfo{
					GroupResource: schema.GroupResource{ // TODO: infer resource from configuration
						Group:    "",
						Resource: "pod",
					},
					Namespaced: true,
					Metric:     field.Name,
				})
			}
		}
	}

	for _, metricSet := range metricSets {
		if err := getMappingFor(metricSet, esClient, metricRecoder); err != nil {
			return nil, nil, err
		}
	}
	return metricRecoder.metrics, metricRecoder.indexedMetrics, nil
}

func getMappingFor(metricSet config.MetricSet, esClient *esv7.Client, recorder *recorder) error {
	req := esapi.IndicesGetMappingRequest{Index: metricSet.Indices}
	res, err := req.Do(context.Background(), esClient)
	if err != nil {
		return fmt.Errorf("discevery error, got response: %s", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("[%s] Error getting index mapping %v", res.Status(), metricSet.Indices)
	} else {
		// Deserialize the response into a map.
		var r map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
			return fmt.Errorf("error parsing the response body: %s", err)
		} else {
			// Process mapping
			for _, indexMapping := range r {
				m := indexMapping.(map[string]interface{})
				mapping, hasMapping := m["mappings"]
				if !hasMapping {
					return fmt.Errorf("discovery error: no 'mapping' field in %s", metricSet.Indices)
				}
				recorder.processMappingDocument(mapping, metricSet.Fields, metricSet.Indices)
			}
		}
	}
	return nil
}

func (r *recorder) processMappingDocument(mapping interface{}, fields config.FieldsSet, indices []string) {
	tm, ok := mapping.(map[string]interface{})
	if !ok {
		return
	}
	rp := tm["properties"]
	rpm, ok := rp.(map[string]interface{})
	if !ok {
		return
	}
	r._processMappingDocument("", rpm, fields, indices)
}

func newRecorder(esProvider provider.MetricsProvider) *recorder {
	return &recorder{
		esProvider:     esProvider,
		indexedMetrics: make(map[string]common.MetricMetadata),
	}
}

type recorder struct {
	esProvider     provider.MetricsProvider
	metrics        []provider.CustomMetricInfo
	indexedMetrics map[string]common.MetricMetadata
}

func (r *recorder) _processMappingDocument(root string, d map[string]interface{}, fieldsSet config.FieldsSet, indices []string) {
	for k, t := range d {
		if k == "*" {
			return
		}
		if k == "properties" {
			tm, ok := t.(map[string]interface{})
			if !ok {
				continue
			}
			r._processMappingDocument(root, tm, fieldsSet, indices)
		} else {
			// Is there a properties child ?
			child, ok := t.(map[string]interface{})
			if !ok {
				continue
			}
			if _, hasProperties := child["properties"]; hasProperties {
				var newRoot string
				if root == "" {
					newRoot = k
				} else {
					newRoot = fmt.Sprintf("%s.%s", root, k)
				}
				r._processMappingDocument(newRoot, child, fieldsSet, indices)
			} else {
				// New metric
				metricName := fmt.Sprintf("%s.%s", root, k)
				fields := fieldsSet.FindMetadata(metricName)
				if fields == nil {
					// field does not match a pattern, do not register it as available
					continue
				}
				r.metrics = append(r.metrics, provider.CustomMetricInfo{
					GroupResource: schema.GroupResource{ // TODO: infer resource from configuration
						Group:    "",
						Resource: "pod",
					},
					Namespaced: true,
					Metric:     metricName,
				})
				r.indexedMetrics[metricName] = common.MetricMetadata{
					MetricsProvider: r.esProvider,
					Fields:          *fields,
					Indices:         indices,
				}
			}
		}
	}
}
