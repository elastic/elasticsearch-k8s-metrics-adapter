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

VERSION   ?= $(shell cat VERSION)
REGISTRY  ?= docker.elastic.co
NAMESPACE ?= elasticsearch-k8s-metrics-adapter
IMAGE     ?= elasticsearch-metrics-adapter
TEMP_DIR  := $(shell mktemp -d)
ARCH      ?= amd64

.PHONY: all docker-build build-elasticsearch-k8s-metrics-adapter test test-adapter-container go-run

all: build-elasticsearch-k8s-metrics-adapter check-license-header

build-elasticsearch-k8s-metrics-adapter: check-license-header generated/openapi/zz_generated.openapi.go
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -o elasticsearch-k8s-metrics-adapter github.com/elastic/elasticsearch-k8s-metrics-adapter

tidy:
	go mod tidy

test:
	CGO_ENABLED=0 go test -coverprofile=./coverage.out ./pkg/...

test-kind:
	kind load docker-image $(REGISTRY)/$(NAMESPACE)/$(IMAGE)-$(ARCH):$(VERSION)
	kubectl apply -f deploy/elasticsearch-k8s-metrics-adapter.yaml
	kubectl rollout restart -n custom-metrics deployment/custom-metrics-apiserver

check-license-header:
	@ hack/check/check-license-header.sh

generate-notice-file:
	go install go.elastic.co/go-licence-detector@v0.3.0
	go list -mod=mod -m -json all | go-licence-detector -noticeOut=NOTICE.txt -noticeTemplate=hack/notice/NOTICE.txt.tmpl -includeIndirect -overrides=hack/notice/overrides/overrides.json -rules=hack/notice/rules.json

generated/openapi/zz_generated.openapi.go: go.mod go.sum
	go run k8s.io/kube-openapi/cmd/openapi-gen --logtostderr \
	    -i k8s.io/metrics/pkg/apis/custom_metrics,k8s.io/metrics/pkg/apis/custom_metrics/v1beta1,k8s.io/metrics/pkg/apis/custom_metrics/v1beta2,k8s.io/metrics/pkg/apis/external_metrics,k8s.io/metrics/pkg/apis/external_metrics/v1beta1,k8s.io/apimachinery/pkg/apis/meta/v1,k8s.io/apimachinery/pkg/api/resource,k8s.io/apimachinery/pkg/version,k8s.io/api/core/v1 \
	    -h ./hack/boilerplate.go.txt \
	    -p ./generated/openapi \
	    -O zz_generated.openapi \
	    -o ./ \
	    -r /dev/null

docker-build: generated/openapi/zz_generated.openapi.go generate-notice-file check-license-header
	docker build . \
			--progress=plain \
			--build-arg VERSION='$(VERSION)' \
			-t $(REGISTRY)/$(NAMESPACE)/$(IMAGE)-$(ARCH):$(VERSION)

go-run: ## Run the adapter program locally for development purposes.
	go run main.go \
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
		--platform $(BUILD_PLATFORM) \
		-t $(REGISTRY)/$(NAMESPACE)/$(IMAGE):$(VERSION) \
		--push

##@ Helm
.PHONY: validate-helm
validate-helm:
	helm lint helm && helm template helm
