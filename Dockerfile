FROM golang:1.17.2 as builder

WORKDIR /go/src/github.com/elastic/elasticsearch-adapter

COPY ["go.mod", "go.sum", "./"]
COPY generated/       generated/
COPY pkg/       pkg/
COPY vendor/    vendor/
COPY main.go    main.go

RUN CGO_ENABLED=0 GOOS=linux go build -o elasticsearch-adapter github.com/elastic/elasticsearch-adapter

FROM registry.access.redhat.com/ubi8/ubi-minimal:8.4

ARG VERSION

LABEL name="Elasticsearch Adapter for the Kubernetes Metrics API" \
      io.k8s.display-name="Elasticsearch " \
      maintainer="eck@elastic.co" \
      vendor="Elastic" \
      version="$VERSION" \
      url="https://github.com/elastic/elasticsearch-k8s-metrics-adapter/" \
      summary="The Elasticsearch adapter for the K8S metrics APIs is an implementation of the Kubernetes resource metrics and custom metrics APIs." \
      description="The Elasticsearch adapter can be used to automatically scale applications, using the Horizontal Pod Autoscaler, querying metrics collected by Metricbeat or Agent and stored in an Elasticsearch cluster." \
      io.k8s.description="The Elasticsearch adapter can be used to automatically scale applications, using the Horizontal Pod Autoscaler, querying metrics collected by Metricbeat or Agent and stored in an Elasticsearch cluster."

# Update the base image packages to the latest versions
RUN microdnf update --setopt=tsflags=nodocs && microdnf clean all

COPY --from=builder /go/src/github.com/elastic/elasticsearch-adapter/elasticsearch-adapter /
ENTRYPOINT ["/elasticsearch-adapter", "--logtostderr=true"]
