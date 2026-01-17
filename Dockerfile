FROM docker.elastic.co/wolfi/go:1.25.5-r0@sha256:268f594045123340cdbb6b510b183f0a12c78208e0bf41991dcfd4337e1a8921 as builder

ARG VERSION
ARG SOURCE_COMMIT

WORKDIR /go/src/github.com/elastic/elasticsearch-k8s-metrics-adapter

COPY ["go.mod", "go.sum", "./"]
COPY generated/       generated/
COPY pkg/       pkg/
COPY main.go    main.go

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X main.serviceVersion=$(echo $SOURCE_COMMIT | cut -c 1-12)" -o elasticsearch-k8s-metrics-adapter github.com/elastic/elasticsearch-k8s-metrics-adapter

FROM docker.elastic.co/wolfi/static:latest@sha256:3348c5f7b97a4d63944034a8c6c43ad8bc69771b2564bed32ea3173bc96b4e04

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
