// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE.txt file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package config

import (
	"errors"
	"io/ioutil"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

type HTTPClientConfig struct {
	Host    string       `yaml:"host"`
	Timeout *v1.Duration `yaml:"timeout,omitempty"`

	AuthenticationConfig *AuthenticationConfig `yaml:"authentication,omitempty"`
	TLSClientConfig      *TLSClientConfig      `yaml:"tls,omitempty"`
}

type AuthenticationConfig struct {
	// Basic authentication
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`
	// Bearer
	BearerTokenFile string `yaml:"tokenFile,omitempty"`
	// TLS client certificate authentication
	CertFile string `yaml:"certFile,omitempty"`
	// TLS client certificate key authentication
	KeyFile string `yaml:"keyFile,omitempty"`
}

func (AuthenticationConfig) GoString() string {
	return "config.AuthenticationConfig(--- REDACTED ---)"
}
func (AuthenticationConfig) String() string {
	return "config.AuthenticationConfig(--- REDACTED ---)"
}

// TLSClientConfig contains settings to enable transport layer security.
type TLSClientConfig struct {
	Insecure bool `yaml:"insecureSkipTLSVerify"` // insecureSkipTLSVerify to match original APIService setting

	// Trusted root certificates for server
	CAFile string `yaml:"caFile,omitempty"`
}

func readFileOrDie(filename string) []byte {
	if filename == "" {
		return nil
	}
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		klog.Fatalf("unable to retrieve data from file: %s", err)
	}
	return data
}

// NewRestClientConfig converts the config provided by the user into a restclient.Config
func (hc *HTTPClientConfig) NewRestClientConfig(client *kubernetes.Clientset, def *restclient.Config) (*restclient.Config, error) {
	// defensive copy
	def = restclient.CopyConfig(def)
	if hc == nil {
		return def, nil
	}
	config := restclient.Config{}
	if hc.Host == "" {
		return nil, errors.New("host is a mandatory field")
	}
	config.Host = hc.Host
	if hc.Timeout != nil {
		config.Timeout = hc.Timeout.Duration
	}
	config.APIPath = def.APIPath

	// TLS fields
	if hc.TLSClientConfig == nil {
		// Reuse default values
		config.TLSClientConfig = def.TLSClientConfig
	} else {
		config.TLSClientConfig.Insecure = hc.TLSClientConfig.Insecure
		config.TLSClientConfig.CAFile = hc.TLSClientConfig.CAFile
	}

	// Authentication fields
	if hc.AuthenticationConfig == nil {
		config.TLSClientConfig.CertData = def.CertData
		config.TLSClientConfig.KeyData = def.KeyData
		config.BearerTokenFile = def.BearerTokenFile
	} else {
		config.TLSClientConfig.CertFile = hc.AuthenticationConfig.CertFile
		config.TLSClientConfig.KeyFile = hc.AuthenticationConfig.KeyFile
		config.BearerTokenFile = hc.AuthenticationConfig.BearerTokenFile
	}
	config.UserAgent = "Elasticsearch Metrics Adapter/0.0.1"

	// Inherit provider config, mostly use in dev mode
	config.AuthProvider = def.AuthProvider
	config.AuthConfigPersister = def.AuthConfigPersister
	config.ExecProvider = def.ExecProvider

	return &config, nil
}
