VERSION ?= $(shell cat VERSION)
REGISTRY?=gcr.io/elastic-cloud-dev/$(USER)
IMAGE?=elasticsearch-metrics-adapter
TEMP_DIR:=$(shell mktemp -d)
ARCH?=amd64

OPENAPI_PATH=./vendor/k8s.io/kube-openapi

VERSION?=latest

.PHONY: all docker-build build-elasticsearch-adapter test test-adapter-container

all: build-elasticsearch-adapter
build-elasticsearch-adapter: vendor generated/openapi/zz_generated.openapi.go
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -o elasticsearch-adapter github.com/elastic/elasticsearch-adapter

vendor: tidy
	go mod vendor

tidy:
	go mod tidy

test:
	CGO_ENABLED=0 go test ./pkg/...

test-kind:
	kind load docker-image $(REGISTRY)/$(IMAGE)-$(ARCH):$(VERSION)
	kubectl apply -f deploy/elasticsearch-adapter.yaml
	kubectl rollout restart -n custom-metrics deployment/custom-metrics-apiserver

generated/openapi/zz_generated.openapi.go: go.mod go.sum
	go run vendor/k8s.io/kube-openapi/cmd/openapi-gen/openapi-gen.go --logtostderr \
	    -i k8s.io/metrics/pkg/apis/custom_metrics,k8s.io/metrics/pkg/apis/custom_metrics/v1beta1,k8s.io/metrics/pkg/apis/custom_metrics/v1beta2,k8s.io/metrics/pkg/apis/external_metrics,k8s.io/metrics/pkg/apis/external_metrics/v1beta1,k8s.io/apimachinery/pkg/apis/meta/v1,k8s.io/apimachinery/pkg/api/resource,k8s.io/apimachinery/pkg/version,k8s.io/api/core/v1 \
	    -h ./hack/boilerplate.go.txt \
	    -p ./generated/openapi \
	    -O zz_generated.openapi \
	    -o ./ \
	    -r /dev/null

docker-build: generated/openapi/zz_generated.openapi.go
	sed -i.bak 's|REGISTRY|'${REGISTRY}'|g' deploy/elasticsearch-adapter.yaml
	rm -rf $(TEMP_DIR) deploy/elasticsearch-adapter.yaml.bak
	docker build . \
			--progress=plain \
			--build-arg VERSION='$(VERSION)' \
			-t $(REGISTRY)/$(IMAGE)-$(ARCH):$(VERSION)