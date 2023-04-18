# Deploy the Elasticsearch Metrics Adapter

## Requirements

The following document assumes that an Elasticsearch cluster is already deployed, with Agent or Beats collecting and shipping metrics, and you want these metrics to be available through the Kubernetes custom metrics API. We also assume that you have installed `helm` to generate the Kubernetes manifests.

## Create a Secret containing the Elasticsearch credentials

The sample configuration file in `values.yaml` assumes that the Elasticsearch URL, username and password are available as environment variables. By default, these environment variables are expected to be injected from a `Secret` named `hpa-elasticsearch-credentials`. Create a `Secret` that will be used to inject these variables into the metrics adapter Pod:

```commandline
kubectl create secret generic hpa-elasticsearch-credentials \
--from-literal HPA_ELASTICSEARCH_USERNAME=<USERNAME> \
--from-literal HPA_ELASTICSEARCH_PASSWORD=<PASSWORD> \
--from-literal HPA_ELASTICSEARCH_HOST=https://<URL>:<PORT> \
-n elastic-observability
```

The provided url must contain the protocol and the port.

## Deploy the adapter using Helm

```
helm install elasticsearch-k8s-metrics-adapter -n elastic-observability .
```