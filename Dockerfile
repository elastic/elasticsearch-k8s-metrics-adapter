FROM docker.elastic.co/wolfi/go:1.24.1-r2@sha256:6e18c3fd770a781fe68c823c64ba7deae0a73dbc50dbe8e579224563892e50c5 as builder

ARG VERSION
ARG SOURCE_COMMIT

WORKDIR /go/src/github.com/elastic/elasticsearch-k8s-metrics-adapter

COPY ["go.mod", "go.sum", "./"]
COPY generated/       generated/
COPY pkg/       pkg/
COPY main.go    main.go

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X main.serviceVersion=$(echo $SOURCE_COMMIT | cut -c 1-12)" -o elasticsearch-k8s-metrics-adapter github.com/elastic/elasticsearch-k8s-metrics-adapter

FROM docker.elastic.co/wolfi/static:latest@sha256:9276a4ebe6b98cd1bbd53b8139228434a0e4f00d06d39e33688e9bd759986656

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
