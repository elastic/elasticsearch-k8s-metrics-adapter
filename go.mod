module github.com/elastic/elasticsearch-adapter

go 1.16

require (
	github.com/elastic/go-elasticsearch/v7 v7.12.0
	github.com/go-openapi/spec v0.20.0
	github.com/itchyny/gojq v0.12.3
	github.com/kubernetes-sigs/custom-metrics-apiserver v0.0.0-20210311094424-0ca2b1909cdc
	github.com/sirupsen/logrus v1.6.0
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/apimachinery v0.20.0
	k8s.io/apiserver v0.20.0
	k8s.io/client-go v0.20.0
	k8s.io/component-base v0.20.0
	k8s.io/klog/v2 v2.4.0
	k8s.io/kube-openapi v0.0.0-20210113233702-8566a335510f
	k8s.io/metrics v0.20.0
)
