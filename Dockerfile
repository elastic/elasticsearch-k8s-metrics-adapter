FROM docker.elastic.co/wolfi/go:1.25.3-r0@sha256:18f8a17f3c31416e14b929fbb07b2e9850a654ccc884d9463048299651d34f2c as builder

ARG VERSION
ARG SOURCE_COMMIT

WORKDIR /go/src/github.com/elastic/elasticsearch-k8s-metrics-adapter

COPY ["go.mod", "go.sum", "./"]
COPY generated/       generated/
COPY pkg/       pkg/
COPY main.go    main.go

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X main.serviceVersion=$(echo $SOURCE_COMMIT | cut -c 1-12)" -o elasticsearch-k8s-metrics-adapter github.com/elastic/elasticsearch-k8s-metrics-adapter

FROM docker.elastic.co/wolfi/static:latest@sha256:939a132511fcbc2702e0e251b6f3ea368c0ad4f114678ae5973903352357d01a

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
