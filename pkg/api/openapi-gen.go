// +build codegen

// Package is only a stub to ensure k8s.io/kube-openapi/cmd/openapi-gen is vendored
// so the same version of kube-openapi is used to generate and render the openapi spec
package main

import (
	_ "k8s.io/kube-openapi/cmd/openapi-gen"
)
