# Deploy the Elasticsearch Metrics Adapter

## Requirements

The following document assumes that an Elasticsearch cluster is already deployed, with Agent or Beats collecting and shipping metrics, and you want these metrics to be available through the Kubernetes custom metrics API.

## Create the metrics adapter configuration file

The command below creates a sample configuration file in a `ConfigMap`. If you want the Elasticsearch metrics adapter to forward metric requests to an existing custom metric adapter, uncomment the relevant section in this example.

```
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Namespace
metadata:
  name: elastic-custom-metrics
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: elasticsearch-custom-metrics-config
  namespace: elastic-custom-metrics
data:
  config.yml: |-
    metricServers:
      - name: elasticsearch-metrics-cluster
        serverType: elasticsearch
        clientConfig:
          host: ${ELASTICSEARCH_URL}
          authentication:
            username: ${ELASTICSEARCH_USER}
            password: ${ELASTICSEARCH_PASSWORD}
          tls:
            insecureSkipTLSVerify: true
        metricSets:
          - indices: [ 'metrics-*' , 'metricbeat-*' ]
      ## Uncomment the section below to also use an existing custom metrics server:
      #- name: my-existing-metrics-adapter
      #  serverType: custom
      #  clientConfig:
      #    host: https://prometheus-metrics-apiserver.prometheus-custom-metrics.svc
      #    tls:
      #      insecureSkipTLSVerify: true
EOF
```

## Create a Secret containing the Elasticsearch credentials

The configuration file created in the previous section assumes that the Elasticsearch URL, username and password are available as environment variables. Create a `Secret` that will be used to inject these variables into the metrics adapter Pod:

```
kubectl create secret generic -n elastic-custom-metrics elasticsearch \
--from-literal=url=https://elasticsearch-es-http.namespace.svc:9200 \
--from-literal=user=<USERNAME> \
--from-literal=password=<PASSWORD>
```

The provided url must contain the protocol and the port.

## Create the roles, deployment and update the APIService

Create the required Kubernetes resources and deploy the adapter as part of a `Deployment` with the following command:

`kubectl apply -f https://raw.githubusercontent.com/elastic/elasticsearch-k8s-metrics-adapter/main/deploy/deployment.yaml`

The metrics adapter Pod should be eventually running:

```
$ kubectl get pod -n elastic-custom-metrics -l app=elasticsearch-metrics-apiserver
NAME                                               READY   STATUS    RESTARTS   AGE
elasticsearch-metrics-apiserver-6c55666c85-gm7gw   1/1     Running   0          15s
```