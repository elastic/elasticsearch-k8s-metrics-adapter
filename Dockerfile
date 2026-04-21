FROM docker.elastic.co/wolfi/go:v1.26.2-r0@sha256:8ae760d4a7bb419d04944fe8c164de74a824e7374b359cd2f926716a6ebf9116 as builder

ARG VERSION
ARG SOURCE_COMMIT

WORKDIR /go/src/github.com/elastic/elasticsearch-k8s-metrics-adapter

COPY ["go.mod", "go.sum", "./"]
COPY generated/       generated/
COPY pkg/       pkg/
COPY main.go    main.go

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X main.serviceVersion=$(echo $SOURCE_COMMIT | cut -c 1-12)" -o elasticsearch-k8s-metrics-adapter github.com/elastic/elasticsearch-k8s-metrics-adapter

FROM docker.elastic.co/wolfi/static:latest@sha256:1f14279403150757d801f6308bb0f4b816b162fddce10b9bd342f10adc3cf7fa

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
