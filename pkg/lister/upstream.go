package lister

import (
	"strings"

	"github.com/elastic/elasticsearch-adapter/pkg/common"
	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/klog/v2"
)

// upstreamMetrics create a list of metrics served by the upstream server
func upstreamMetrics(
	restClient *discovery.DiscoveryClient,
	metricsProvider provider.MetricsProvider,
) ([]provider.CustomMetricInfo, map[string]common.MetricMetadata, error) {
	gv := schema.GroupVersion{
		Group:   "custom.metrics.k8s.io",
		Version: "v1beta2",
	}.String()
	list, err := restClient.ServerResourcesForGroupVersion(gv)
	if err != nil {
		klog.Errorf("Error while loading metrics list from upstream: %s", err)
		return nil, nil, err
	}
	// Convert APIResources to []provider.CustomMetricInfo
	upstreamMetrics := make([]provider.CustomMetricInfo, len(list.APIResources))
	for i, resource := range list.APIResources {
		resourceName := strings.Split(resource.Name, "/")
		if len(resourceName) != 2 {
			klog.Errorf("Unable to parse upstream metric %s", resource.Name)
			continue
		}
		upstreamMetrics[i] = provider.CustomMetricInfo{
			GroupResource: schema.ParseGroupResource(resourceName[0]),
			Namespaced:    resource.Namespaced,
			Metric:        resourceName[1],
		}
	}

	indexedMetrics := make(map[string]common.MetricMetadata, len(upstreamMetrics))
	for _, m := range upstreamMetrics {
		indexedMetrics[m.Metric] = common.MetricMetadata{
			MetricsProvider: metricsProvider,
		}
	}
	return upstreamMetrics, indexedMetrics, nil
}
