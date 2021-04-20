// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. Elasticsearch B.V. licenses this file to
// you under the Apache License, Version 2.0 (the "License");
// you may  not use this file except in compliance with the
// License.
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
	"context"
	"errors"
	"fmt"

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
	Username string     `yaml:"username,omitempty"`
	Password *SecretRef `yaml:"password,omitempty"`
	// Bearer
	BearerToken     *SecretRef `yaml:"token,omitempty"`
	BearerTokenFile string     `yaml:"tokenFile,omitempty"`
	// TLS client certificate authentication
	Cert *SecretRef `yaml:"cert,omitempty"`
	// TLS client certificate key authentication
	Key *SecretRef `yaml:"key,omitempty"`
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

	// ServerName is passed to the server for SNI and is used in the client to check server
	// certificates against. If ServerName is empty, the hostname used to contact the
	// server is used.
	ServerName string `yaml:"serverName"`

	// Trusted root certificates for server
	CA *SecretRef `yaml:"ca,omitempty"`
}

type SecretRef struct {
	Name      string `yaml:"name,omitempty"`
	Namespace string `yaml:"namespace,omitempty"`
	Key       string `yaml:"key,omitempty"`
}

func (sr *SecretRef) Data(client *kubernetes.Clientset) ([]byte, error) {
	if sr == nil {
		return nil, nil
	}
	// Read the secret
	secret, err := client.CoreV1().Secrets(sr.Name).Get(context.TODO(), sr.Name, v1.GetOptions{})
	if err != nil {
		return nil, err
	}
	data, keyExists := secret.Data[sr.Key]
	if !keyExists {
		return nil, fmt.Errorf("key %s not found in %s/%s", sr.Key, sr.Namespace, sr.Name)
	}
	return data, nil
}

func (sr *SecretRef) DataOrDie(client *kubernetes.Clientset) []byte {
	data, err := sr.Data(client)
	if err != nil {
		klog.Fatalf("unable to retrieve data from secret: %v", err)
	}
	return data
}

// ClientConfig converts the config provided by the user into a restclient.Config
func (hc *HTTPClientConfig) ClientConfig(client *kubernetes.Clientset, def *restclient.Config) (*restclient.Config, error) {
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
		config.TLSClientConfig.CAData = hc.TLSClientConfig.CA.DataOrDie(client)
		config.TLSClientConfig.ServerName = hc.TLSClientConfig.ServerName
	}

	// Authentication fields
	if hc.AuthenticationConfig == nil {
		config.TLSClientConfig.CertData = def.CertData
		config.TLSClientConfig.KeyData = def.KeyData
		config.BearerToken = def.BearerToken
		config.BearerTokenFile = def.BearerTokenFile
	} else {
		config.TLSClientConfig.CertData = hc.AuthenticationConfig.Cert.DataOrDie(client)
		config.TLSClientConfig.KeyData = hc.AuthenticationConfig.Cert.DataOrDie(client)
		config.BearerToken = string(hc.AuthenticationConfig.BearerToken.DataOrDie(client))
		config.BearerTokenFile = hc.AuthenticationConfig.BearerTokenFile
	}
	config.UserAgent = "Elasticsearch Metrics Adapter/0.0.1"

	// Inherit provider config, mostly use in dev mode
	config.AuthProvider = def.AuthProvider
	config.AuthConfigPersister = def.AuthConfigPersister
	config.ExecProvider = def.ExecProvider

	return &config, nil
}
