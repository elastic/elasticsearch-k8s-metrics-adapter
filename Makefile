# Licensed to Elasticsearch B.V. under one or more contributor
# license agreements. See the NOTICE.txt file distributed with
# this work for additional information regarding copyright
# ownership. Elasticsearch B.V. licenses this file to you under
# the Apache License, Version 2.0 (the "License"); you may
# not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.

VERSION     ?= $(shell cat VERSION)
REGISTRY    ?= docker.elastic.co
NAMESPACE   ?= elasticsearch-k8s-metrics-adapter
IMAGE       ?= elasticsearch-metrics-adapter
TEMP_DIR    := $(shell mktemp -d)
ARCH        ?= amd64
SHA1        ?= $(shell git rev-parse --short=12 --verify HEAD)
GO_LD_FLAGS := -X main.serviceVersion=$(SHA1)


.PHONY: all docker-build build-elasticsearch-k8s-metrics-adapter test test-adapter-container go-run

all: build-elasticsearch-k8s-metrics-adapter check-license-header

build-elasticsearch-k8s-metrics-adapter: check-license-header generated/openapi/zz_generated.openapi.go
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -ldflags "$(GO_LD_FLAGS)" -o elasticsearch-k8s-metrics-adapter github.com/elastic/elasticsearch-k8s-metrics-adapter

tidy:
	go mod tidy

test: toolchain
	CGO_ENABLED=0 go test -coverprofile=./coverage.out ./pkg/...

test-kind:
	kind load docker-image $(REGISTRY)/$(NAMESPACE)/$(IMAGE)-$(ARCH):$(VERSION)
	kubectl apply -f deploy/elasticsearch-k8s-metrics-adapter.yaml
	kubectl rollout restart -n custom-metrics deployment/custom-metrics-apiserver

.PHONY: toolchain
toolchain:
	go env -w GOTOOLCHAIN=go1.25.4+auto # temp workaround until https://github.com/golang/go/issues/75031 is fixed

check-license-header:
	@ hack/check/check-license-header.sh

generate-notice-file:
	go install go.elastic.co/go-licence-detector@v0.3.0
	go list -mod=mod -m -json all | go-licence-detector -noticeOut=NOTICE.txt -noticeTemplate=hack/notice/NOTICE.txt.tmpl -includeIndirect -overrides=hack/notice/overrides/overrides.json -rules=hack/notice/rules.json

generated/openapi/zz_generated.openapi.go: go.mod go.sum
	go run k8s.io/kube-openapi/cmd/openapi-gen --logtostderr \
	    --go-header-file ./hack/boilerplate.go.txt \
	    --output-dir ./generated/openapi \
	    --output-pkg github.com/elastic/elasticsearch-k8s-metrics-adapter/generated/openapi \
	    --output-file zz_generated.openapi.go \
	    --report-filename /dev/null \
	    k8s.io/metrics/pkg/apis/custom_metrics k8s.io/metrics/pkg/apis/custom_metrics/v1beta1 k8s.io/metrics/pkg/apis/custom_metrics/v1beta2 k8s.io/metrics/pkg/apis/external_metrics k8s.io/metrics/pkg/apis/external_metrics/v1beta1 k8s.io/apimachinery/pkg/apis/meta/v1 k8s.io/apimachinery/pkg/api/resource k8s.io/apimachinery/pkg/version k8s.io/api/core/v1

docker-build: generated/openapi/zz_generated.openapi.go generate-notice-file check-license-header
	docker build . \
			--progress=plain \
			--build-arg VERSION='$(VERSION)' \
			--build-arg SOURCE_COMMIT='$(SHA1)' \
			-t $(REGISTRY)/$(NAMESPACE)/$(IMAGE)-$(ARCH):$(VERSION)

go-run: ## Run the adapter program locally for development purposes.
	go run -ldflags "$(GO_LD_FLAGS)" main.go \
		--lister-kubeconfig ${HOME}/.kube/config \
		--authentication-kubeconfig ${HOME}/.kube/config \
		--authorization-kubeconfig ${HOME}/.kube/config \
		--secure-port=6443 \
		--v=2 \
		--insecure ## Allow unauthenticated calls

BUILD_PLATFORM ?= "linux/amd64,linux/arm64"

docker-multiarch-build: generated/openapi/zz_generated.openapi.go generate-notice-file check-license-header
	docker buildx build . \
		--progress=plain \
		--build-arg VERSION='$(VERSION)' \
		--build-arg SOURCE_COMMIT='$(SHA1)' \
		--platform $(BUILD_PLATFORM) \
		-t $(REGISTRY)/$(NAMESPACE)/$(IMAGE):$(VERSION) \
		--push

##@ Helm
.PHONY: validate-helm
validate-helm:
	helm lint helm && helm template helm

##@ E2E (kind)
E2E_CLUSTER ?= elasticsearch-adapter-e2e
E2E_CONTEXT := kind-$(E2E_CLUSTER)
E2E_ARCH    ?= $(shell go env GOARCH)
E2E_ADAPTER_IMAGE := elasticsearch-k8s-metrics-adapter:e2e
E2E_MOCKES_IMAGE  := mockes:e2e

.PHONY: e2e-adapter-image e2e-mockes-image e2e-up e2e e2e-down

# Build the adapter image for e2e from the committed sources (no notice/openapi
# regeneration); reuses the production Dockerfile.
e2e-adapter-image: generated/openapi/zz_generated.openapi.go
	docker build . \
		--build-arg VERSION=e2e \
		--build-arg SOURCE_COMMIT='$(SHA1)' \
		-t $(E2E_ADAPTER_IMAGE)

# Build the mock Elasticsearch: compile the (stdlib-only) binary on the host for
# the cluster arch, then package it into a scratch image.
e2e-mockes-image:
	cd it/mockes && CGO_ENABLED=0 GOOS=linux GOARCH=$(E2E_ARCH) go build -o mockes .
	docker build -t $(E2E_MOCKES_IMAGE) it/mockes

# Provision the full e2e environment: kind cluster, images, mock ES, the
# pre-existing HPA (scenario 1), and the adapter in hpa mode.
e2e-up: e2e-adapter-image e2e-mockes-image
	kind create cluster --name $(E2E_CLUSTER) --config it/kind-config.yaml
	kind load docker-image $(E2E_ADAPTER_IMAGE) --name $(E2E_CLUSTER)
	kind load docker-image $(E2E_MOCKES_IMAGE) --name $(E2E_CLUSTER)
	kubectl --context $(E2E_CONTEXT) apply -f it/testdata/externalsecret-crd-stub.yaml
	kubectl --context $(E2E_CONTEXT) create namespace metrics-adapter
	kubectl --context $(E2E_CONTEXT) apply -f it/testdata/mockes.yaml
	kubectl --context $(E2E_CONTEXT) wait --for=condition=available --timeout=120s \
		-n metrics-adapter deployment/mock-elasticsearch
	kubectl --context $(E2E_CONTEXT) apply -f it/testdata/hpa-startup.yaml
	helm --kube-context $(E2E_CONTEXT) install metrics-adapter ./helm \
		-n metrics-adapter -f it/testdata/values-e2e.yaml

# Run the e2e suite against the provisioned cluster.
e2e:
	go test -tags e2e -count=1 -v -timeout 10m ./it/e2e/...

e2e-down:
	kind delete cluster --name $(E2E_CLUSTER)
