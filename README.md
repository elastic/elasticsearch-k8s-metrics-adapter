# Elasticsearch Adapter for the Kubernetes Metrics APIs

The Elasticsearch adapter for the K8S metrics APIs is an implementation of the Kubernetes
[resource metrics](https://github.com/kubernetes/community/blob/master/contributors/design-proposals/instrumentation/resource-metrics-api.md) and
[custom metrics](https://github.com/kubernetes/community/blob/master/contributors/design-proposals/instrumentation/custom-metrics-api.md) APIs.

It can be used to automatically scale applications, using the [Horizontal Pod Autoscaler](https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale) querying metrics collected by [Metricbeat](https://www.elastic.co/beats/metricbeat) or [Agent](https://www.elastic.co/elastic-agent), and stored in [Elasticsearch](https://www.elastic.co/elasticsearch/).

## Use of the adapter
This adapter is currently considered as experimental, and is thus not yet fully supported in production by Elastic.
We'd love to get your feedback for any issues or contributions, in the form of issues in this repository.

## Getting started

Refer to [helm/README.md](helm/README.md) to configure and deploy the metrics adapter to your Kubernetes cluster.

## Configuration

### Metrics discovery

The `metricSets` section contains the information required to discover the metrics exposed by the adapter:

```yaml
metricSets:
  - indices: [ 'metricbeat-*' ] # set of regular expressions to list the indices to be searched
    fields:
      - patterns: [ '^prometheus\.metrics\.' ] # set of regular expressions to list the fields exposed as metrics by the server
      - patterns: [ '^kibana\.stats\.' ] # because we need Kibana metrics for the example below
```

### Compute advanced metrics

Complex metrics can be calculated using a custom query, for example:

```yaml
  - name: "my-computed-metric" # name of the metric as it will be exposed to Kubernetes
    search: # This is an example of an advanced custom search, calculated using an aggregation.
      metricPath: ".aggregations.custom_name.buckets.[0].pod_load.value" # Path to the metric value.
      timestampPath: ".aggregations.custom_name.buckets.[0].timestamp.value_as_string" # Path to the timestamp.
      body: >
        {
          "query": {
            ... your query ...
          },
          "size": 0,
          "aggs": {
            "custom_name": {
              "terms": {
                "field": "kibana.stats.name"
              },
              "aggs": {
                "pods_count": {
                  "cardinality": {
                    "field": "kubernetes.pod.name"
                  }
                },
                "maxLoad": {
                  "max": {
                    "field": "kibana.stats.load"
                  }
                },
                "timestamp": {
                  "max": {
                    "field": "@timestamp"
                  }
                },
                "pod_load": {
                  "bucket_script": {
                    "buckets_path": {
                      "load": "maxLoad"
                    },
                    "script": "params.load / {{ len .Objects }}"
                  }
                }
              }
            }
          }
        }
```

The `body` field must contain a valid Elasticsearch query. `metricPath` and `timestampPath` must contain valid [JQ queries](https://stedolan.github.io/jq/manual/#Basicfilters) used to get the metric value and the timestamp from the Elasticsearch response.

### Forwarding metrics request to existing metrics adapters

You may want to also serve some metrics from an existing third party metric server like Prometheus or Stackdriver. This can be done by adding the third party adapter API endpoint to the `metricServers` list:

```yaml
## Metric server of type "custom" can be used to reference an existing or third party metric adapter service.
metricServers:
  - name: my-existing-metrics-adapter
    serverType: custom # To be used to forward metric requests to a server which complies with https://github.com/kubernetes/metrics
    clientConfig:
      host: https://custom-metrics-apiserver.custom-metrics.svc
      tls:
        insecureSkipTLSVerify: true
```

## Example

The example below assumes that:
1. Your Kubernetes cluster is running [Elastic Cloud on Kubernetes](https://www.elastic.co/guide/en/cloud-on-k8s/current/index.html) 1.7.0 (or later) which implements the `/scale` endpoint on Kibana.
1. A `Kibana` resource named `kibana-example` is deployed.
2. Kibana metrics are collected using the Metricbeat [Kibana module](https://www.elastic.co/guide/en/beats/metricbeat/current/metricbeat-module-kibana.html) and stored in an Elasticsearch cluster.

> :warning: Metrics collected by Metricbeat must be sent to `metricbeat-*` indices, `xpack.enabled` must be set to its default value: `false`

The example below shows how an `HorizontalPodAutoscaler` resource can be created to scale the aforementioned Kibana automatically according to the average number of concurrent connections:

```yaml
apiVersion: autoscaling/v2beta2
kind: HorizontalPodAutoscaler
metadata:
  name: kibana-hpa-example
  namespace: my-namespace
spec:
  minReplicas: 1
  maxReplicas: 3
  metrics:
  - type: Pods
    pods:
      metric:
        name: kibana.stats.concurrent_connections
      target:
        type: AverageValue
        averageValue: 42
  scaleTargetRef:
    apiVersion: kibana.k8s.elastic.co/v1
    kind: Kibana
    name: kibana-example
  behavior:
    scaleDown:
      stabilizationWindowSeconds: 5
```

The Kubernetes Horizontal Pod Autoscaler is now able to scale Kibana to ensure that each Pod has at most 42 _(as an average)_ concurrent connections:

```yaml
% kubectl get hpa -w
NAME                 REFERENCE               TARGETS        MINPODS   MAXPODS   REPLICAS   AGE
kibana-hpa-example   Kibana/kibana-example   12/42          1         3         1          17s
kibana-hpa-example   Kibana/kibana-example   49/42          1         3         1          63s
kibana-hpa-example   Kibana/kibana-example   49/42          1         3         2          80s
kibana-hpa-example   Kibana/kibana-example   26/42          1         3         2          112s
kibana-hpa-example   Kibana/kibana-example   104500m/42     1         3         3          2m23s
kibana-hpa-example   Kibana/kibana-example   13/42          1         3         1          3m26s
```

## Troubleshooting

### Calling the Custom Metrics API (like the Kubernetes control plane would)

You can call the Custom Metrics API from your local workstation to check what metrics are exposed and their current values.

In a first terminal start the `kubectl proxy` command:

```bash
% kubectl proxy
Starting to serve on 127.0.0.1:8001

```

From another terminal (or from your favorite API client) you can now run the following commands.

#### Listing all available metrics

```bash
% curl http://127.0.0.1:8001/apis/custom.metrics.k8s.io/v1beta2/
```

```json
{
  "kind": "APIResourceList",
  "apiVersion": "v1",
  "groupVersion": "custom.metrics.k8s.io/v1beta2",
  "resources": [
    {
      "name": "pods/system.memory.hugepages.total",
      "singularName": "",
      "namespaced": true,
      "kind": "MetricValueList",
      "verbs": [
        "get"
      ]
    },
    {
      "name": "pods/aws.rds.storage_used.backup_retention_period.bytes",
      "singularName": "",
      "namespaced": true,
      "kind": "MetricValueList",
      "verbs": [
        "get"
      ]
    },
    {
      "name": "pods/postgresql.bgwriter.buffers.clean",
      "singularName": "",
      "namespaced": true,
      "kind": "MetricValueList",
      "verbs": [
        "get"
      ]
    },
[...]
```

#### Request a specific metric using a label selector

Metrics are requested by the Kubernetes control plane using 3 parameters:
1. The **name of the metric**.
2. The **namespace** in which the `Pods` are running.
3. A **label selector** to retrieve the list of Pods.

You can run the following command to get the value of the metric `kibana.stats.concurrent_connections` for all the `Pods` that match the label selector `common.k8s.elastic.co/type=kibana,kibana.k8s.elastic.co/name=kb` in namespace `my-ns`:

```bash
% curl 'http://127.0.0.1:8001/apis/custom.metrics.k8s.io/v1beta2/namespaces/my-ns/pods/*/kibana.stats.concurrent_connections?labelSelector=common.k8s.elastic.co%2Ftype%3Dkibana%2Ckibana.k8s.elastic.co%2Fname%3Dkb'
```

```json
{
  "kind": "MetricValueList",
  "apiVersion": "custom.metrics.k8s.io/v1beta2",
  "metadata": {},
  "items": [
    {
      "describedObject": {
        "kind": "Pod",
        "namespace": "my-ns",
        "name": "kb-background-tasks-kb-dcfff65c9-qxbw2",
        "apiVersion": "/v1"
      },
      "metric": {
        "name": "kibana.stats.concurrent_connections",
        "selector": null
      },
      "timestamp": "2023-04-03T09:20:48Z",
      "value": "10"
    },
    {
      "describedObject": {
        "kind": "Pod",
        "namespace": "my-ns",
        "name": "kb-ui-kb-77764d6f59-75hqz",
        "apiVersion": "/v1"
      },
      "metric": {
        "name": "kibana.stats.concurrent_connections",
        "selector": null
      },
      "timestamp": "2023-04-03T09:20:46Z",
      "value": "10"
    }
  ]
}
```


### HorizontalPodAutoscaler events

The `describe` API on the `HorizontalPodAutoscaler` is a good starting point to understand the behaviour or the root cause of an issue with the adapter.

```shell
% kubectl describe hpa kibana-hpa-example
Name:                                             kibana-hpa-example
Namespace:                                        stack-monitoring
Reference:                                        Kibana/kibana-example
Metrics:                                          ( current / target )
  "kibana.stats.concurrent_connections" on pods:  13 / 42
Min replicas:                                     1
Max replicas:                                     3
Behavior:
  Scale Up:
    Stabilization Window: 0 seconds
    Select Policy: Max
    Policies:
      - Type: Pods     Value: 4    Period: 15 seconds
      - Type: Percent  Value: 100  Period: 15 seconds
  Scale Down:
    Stabilization Window: 5 seconds
    Select Policy: Max
    Policies:
      - Type: Percent  Value: 100  Period: 15 seconds
Kibana pods:           1 current / 1 desired
Conditions:
  Type            Status  Reason              Message
  ----            ------  ------              -------
  AbleToScale     True    ReadyForNewScale    recommended size matches current size
  ScalingActive   True    ValidMetricFound    the HPA was able to successfully calculate a replica count from pods metric kibana.stats.concurrent_connections
  ScalingLimited  False   DesiredWithinRange  the desired count is within the acceptable range
Events:
  Type     Reason                        Age                From                       Message
  ----     ------                        ----               ----                       -------
  Normal   SuccessfulRescale             3m20s              horizontal-pod-autoscaler  New size: 2; reason: pods metric kibana.stats.concurrent_connections above target
  Warning  FailedGetPodsMetric           3m3s               horizontal-pod-autoscaler  unable to get metric kibana.stats.concurrent_connections: unable to fetch metrics from custom metrics API: no documents in map[_shards:map[failed:0 skipped:0 successful:1 total:1] hits:map[hits:[] max_score:<nil> total:map[relation:eq value:0]] timed_out:false took:45]
  Warning  FailedComputeMetricsReplicas  3m3s               horizontal-pod-autoscaler  failed to compute desired number of replicas based on listed metrics for Kibana/stack-monitoring/kibana: invalid metrics (1 invalid out of 1), first error is: failed to get pods metric value: unable to get metric kibana.stats.concurrent_connections: unable to fetch metrics from custom metrics API: no documents in map[_shards:map[failed:0 skipped:0 successful:1 total:1] hits:map[hits:[] max_score:<nil> total:map[relation:eq value:0]] timed_out:false took:45]
  Normal   SuccessfulRescale             2m17s              horizontal-pod-autoscaler  New size: 3; reason: pods metric kibana.stats.concurrent_connections above target
  Warning  FailedGetPodsMetric           105s (x2 over 2m)  horizontal-pod-autoscaler  unable to get metric kibana.stats.concurrent_connections: unable to fetch metrics from custom metrics API: no documents in map[_shards:map[failed:0 skipped:0 successful:1 total:1] hits:map[hits:[] max_score:<nil> total:map[relation:eq value:0]] timed_out:false took:0]
  Warning  FailedComputeMetricsReplicas  105s (x2 over 2m)  horizontal-pod-autoscaler  failed to compute desired number of replicas based on listed metrics for Kibana/stack-monitoring/kibana: invalid metrics (1 invalid out of 1), first error is: failed to get pods metric value: unable to get metric kibana.stats.concurrent_connections: unable to fetch metrics from custom metrics API: no documents in map[_shards:map[failed:0 skipped:0 successful:1 total:1] hits:map[hits:[] max_score:<nil> total:map[relation:eq value:0]] timed_out:false took:0]
  Normal   SuccessfulRescale             74s                horizontal-pod-autoscaler  New size: 1; reason: All metrics below target
```

Note that `FailedGetPodsMetric` or `FailedComputeMetricsReplicas` events may happen when the resource is being scaled up or scaled down.

### Logs

Logs can be retrieved with the following command:

```shell
% kubectl logs -n elasticsearch-custom-metrics -l app=custom-metrics-apiserver
I0328 10:49:47.017359       1 provider.go:172] ListAllMetrics()
I0328 10:49:47.017699       1 httplog.go:89] "HTTP" verb="GET" URI="/apis/custom.metrics.k8s.io/v1beta2?timeout=32s" latency="3.901543ms" userAgent="kube-controller-manager/v1.18.14 (linux/amd64) kubernetes/44dfef4/system:serviceaccount:kube-system:resourcequota-controller" srcIP="10.28.112.1:51876" resp=200
I0328 10:49:47.065831       1 provider.go:172] ListAllMetrics()
I0328 10:49:47.066100       1 httplog.go:89] "HTTP" verb="GET" URI="/apis/custom.metrics.k8s.io/v1beta1?timeout=32s" latency="3.088834ms" userAgent="kube-controller-manager/v1.18.14 (linux/amd64) kubernetes/44dfef4/system:serviceaccount:kube-system:resourcequota-controller" srcIP="10.28.80.5:33018" resp=200
I0328 10:49:51.540363       1 provider.go:172] ListAllMetrics()
I0328 10:49:51.540363       1 provider.go:172] ListAllMetrics()
I0328 10:49:51.540782       1 httplog.go:89] "HTTP" verb="GET" URI="/apis/custom.metrics.k8s.io/v1beta2?timeout=32s" latency="4.749705ms" userAgent="kubectl/openshift (darwin/amd64) kubernetes/ffd6836" srcIP="10.28.112.1:51876" resp=200
I0328 10:49:51.540878       1 httplog.go:89] "HTTP" verb="GET" URI="/apis/custom.metrics.k8s.io/v1beta1?timeout=32s" latency="4.628104ms" userAgent="kubectl/openshift (darwin/amd64) kubernetes/ffd6836" srcIP="10.28.80.5:33018" resp=200
time="2021-03-28T10:49:55Z" level=info msg="GetMetricBySelector(namespace=stack-monitoring,selector=common.k8s.elastic.co/type=kibana,kibana.k8s.elastic.co/name=kibana-example,info=pods/kibana.stats.concurrent_connections(namespaced),metricSelector=)"
I0328 10:49:55.302521       1 httplog.go:89] "HTTP" verb="GET" URI="/apis/custom.metrics.k8s.io/v1beta2/namespaces/stack-monitoring/pods/%2A/kibana.stats.concurrent_connections?labelSelector=common.k8s.elastic.co%2Ftype%3Dkibana%2Ckibana.k8s.elastic.co%2Fname%3Dkibana-example" latency="34.605841ms" userAgent="vpa-recommender/v0.0.0 (linux/amd64) kubernetes/$Format/kuba-horizontal-pod-autoscaler" srcIP="10.28.80.4:50544" resp=200
```

Verbosity can be increased with the `--v` flag:

```yaml
...
  containers:
    - name: custom-metrics-apiserver
      args:
        - /adapter
        - --secure-port=6443
        - --logtostderr=true
        - --v=9
...
```
