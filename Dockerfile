FROM docker.elastic.co/wolfi/go:v1.27.1-r0@sha256:2b7ef86500efa44dfe2ae92a02c42ef840d216af5ea10c1bcc7a50e8f07f5162 as builder

ARG VERSION
ARG SOURCE_COMMIT

WORKDIR /go/src/github.com/elastic/elasticsearch-k8s-metrics-adapter

COPY ["go.mod", "go.sum", "./"]
COPY generated/       generated/
COPY pkg/       pkg/
COPY main.go    main.go

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X main.serviceVersion=$(echo $SOURCE_COMMIT | cut -c 1-12)" -o elasticsearch-k8s-metrics-adapter github.com/elastic/elasticsearch-k8s-metrics-adapter

FROM docker.elastic.co/wolfi/static:latest@sha256:41e17ed83c594a64a9396b6ab96dd26d5ddc290dacf4c177464712ff21ad534f

LABEL name="Elasticsearch Adapter for the Kubernetes Metrics API" \
      io.k8s.display-name="Elasticsearch " \
      maintainer="eck@elastic.co" \
      vendor="Elastic" \
      version="$VERSION" \
      url="https://github.com/elastic/elasticsearch-k8s-metrics-adapter/" \
      summary="The Elasticsearch adapter for the K8S metrics APIs is an implementation of the Kubernetes resource metrics and custom metrics APIs." \
      description="The Elasticsearch adapter can be used to automatically scale applications, using the Horizontal Pod Autoscaler, querying metrics collected by Metricbeat or Agent and stored in an Elasticsearch cluster." \
      io.k8s.description="The Elasticsearch adapter can be used to automatically scale applications, using the Horizontal Pod Autoscaler, querying metrics collected by Metricbeat or Agent and stored in an Elasticsearch cluster."

COPY --from=builder /go/src/github.com/elastic/elasticsearch-k8s-metrics-adapter/elasticsearch-k8s-metrics-adapter /

# Copy NOTICE.txt and LICENSE.txt into the image
COPY ["NOTICE.txt", "LICENSE.txt", "/licenses/"]

ENTRYPOINT ["/elasticsearch-k8s-metrics-adapter"]
