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

VERSION ?= $(shell cat VERSION)
REGISTRY?=gcr.io/elastic-cloud-dev/$(USER)
IMAGE?=elasticsearch-metrics-adapter
TEMP_DIR:=$(shell mktemp -d)
ARCH?=amd64

OPENAPI_PATH=./vendor/k8s.io/kube-openapi

VERSION?=latest

.PHONY: all docker-build build-elasticsearch-adapter test test-adapter-container

all: build-elasticsearch-adapter check-license-header
build-elasticsearch-adapter: check-license-header vendor generated/openapi/zz_generated.openapi.go
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

check-license-header:
	@ hack/check/check-license-header.sh

generate-notice-file:
	go install go.elastic.co/go-licence-detector@v0.3.0
	go list -mod=mod -m -json all | go-licence-detector -noticeOut=NOTICE.txt -noticeTemplate=hack/notice/NOTICE.txt.tmpl -includeIndirect -overrides=hack/notice/overrides/overrides.json -rules=hack/notice/rules.json

generated/openapi/zz_generated.openapi.go: go.mod go.sum
	go run vendor/k8s.io/kube-openapi/cmd/openapi-gen/openapi-gen.go --logtostderr \
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
			-t $(REGISTRY)/$(IMAGE)-$(ARCH):$(VERSION)

