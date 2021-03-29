# Elasticsearch Adapter for Kubernetes Metrics APIs

> :warning: **This project is a proof of concept** which demonstrates how metrics collected by Metricbeat, and stored in Elasticsearch, can be used by the K8S Horizontal Pod Autoscaler to automatically scale workloads on K8S clusters.

The Elasticsearch adapter for the K8S metrics APIs is an implementation of the Kubernetes
[resource metrics](https://github.com/kubernetes/community/blob/master/contributors/design-proposals/instrumentation/resource-metrics-api.md) and
[custom metrics](https://github.com/kubernetes/community/blob/master/contributors/design-proposals/instrumentation/custom-metrics-api.md) APIs.

It can be used to automatically scale applications, using the [Horizontal Pod Autoscaler](https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale) querying metrics collected by [Metricbeat](https://www.elastic.co/beats/metricbeat) and stored in an [Elasticsearch](https://www.elastic.co/elasticsearch/) cluster.

## Deployment

The [`deploy/elasticsearch-adapter.yaml`](deploy/elasticsearch-adapter.yaml) file contains a manifest to deploy the latest image of the adapter on your Kubernetes cluster.

Before applying this manifest a `Secret` named `elasticsearch` must be created. It must contain the following entries to help the adapter to connect to the Elasticsearch cluster which holds the metrics:
- `username`
- `password`
- `url` - must contain the protocol and the port, for example: https://35.240.36.192:9200

## Configuration

Metrics discovery configuration is [hard coded](pkg/config/default.yaml) at the moment.

The configuration contains the information required to discover the metrics exposed by the adapter:

```yaml
metricSets:
  - indices: [ 'metricbeat-*' ] # set of regular expressions to list the indices to be searched
    fields:
      - patterns: [ '^prometheus\.metrics\.' ] # set of regular expressions to list the fields exposed as metrics by the server
        labels: [ '^prometheus\.labels\.(.*)' ] # only a teaser, not used for the moment
      - patterns: [ '^kibana\.stats\.' ] # because we need Kibana metrics for the example below
```

## Example

The example below assumes that:
1. You have deployed a version of [Elastic Cloud on Kubernetes](https://www.elastic.co/guide/en/cloud-on-k8s/current/index.html) which implements the `/scale` endpoint on Kibana, as the one  available [here](https://github.com/elastic/cloud-on-k8s/compare/master...barkbay:autoscaling/kibana-poc).
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
