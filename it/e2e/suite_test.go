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

//go:build e2e

// Package e2e holds the end-to-end tests for the adapter's hpa discovery mode.
// They run against a kind cluster provisioned by `make e2e-up`; this file owns
// shared clients, preconditions and helpers.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
)

const (
	adapterNamespace  = "metrics-adapter"
	adapterDeployment = "elasticsearch-metrics-apiserver"
	apiServiceV2      = "v1beta2.custom.metrics.k8s.io"
	customMetricsBase = "/apis/custom.metrics.k8s.io/v1beta2"

	// startupMetric is referenced by it/testdata/hpa-startup.yaml and is in the
	// mock's default known-fields set.
	startupMetric = "prometheus.proxy_open_connections.value"
	// metricQueryNamespace is queried for values; kube-system always has pods,
	// which the adapter lists before fetching each pod's metric.
	metricQueryNamespace = "kube-system"
)

var (
	clientset  *kubernetes.Clientset
	restClient rest.Interface
	mockURL    string
)

func TestMain(m *testing.M) {
	mockURL = envOr("E2E_MOCK_URL", "http://localhost:30080")

	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{})

	if os.Getenv("E2E_SKIP_CONTEXT_GUARD") == "" {
		raw, err := loader.RawConfig()
		if err != nil {
			fail("unable to read kubeconfig: %v", err)
		}
		if !strings.HasPrefix(raw.CurrentContext, "kind-") {
			fail("refusing to run: current context %q is not a kind cluster "+
				"(set E2E_SKIP_CONTEXT_GUARD=1 to override)", raw.CurrentContext)
		}
	}

	cfg, err := loader.ClientConfig()
	if err != nil {
		fail("unable to build client config: %v", err)
	}
	clientset, err = kubernetes.NewForConfig(cfg)
	if err != nil {
		fail("unable to build clientset: %v", err)
	}
	restClient = clientset.Discovery().RESTClient()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := waitReady(ctx); err != nil {
		fail("environment not ready: %v", err)
	}

	os.Exit(m.Run())
}

// waitReady blocks until the adapter Deployment is available and the
// custom.metrics.k8s.io APIService reports Available=True. The APIService gate
// matters most: a Ready pod whose aggregation isn't wired up still 404s.
func waitReady(ctx context.Context) error {
	if err := pollUntil(ctx, 5*time.Second, func() (bool, error) {
		d, err := clientset.AppsV1().Deployments(adapterNamespace).Get(ctx, adapterDeployment, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		return d.Status.AvailableReplicas >= 1, nil
	}); err != nil {
		return fmt.Errorf("adapter deployment not available: %w", err)
	}
	if err := pollUntil(ctx, 5*time.Second, func() (bool, error) {
		return apiServiceAvailable(ctx, apiServiceV2)
	}); err != nil {
		return fmt.Errorf("APIService %s not available: %w", apiServiceV2, err)
	}
	return nil
}

func apiServiceAvailable(ctx context.Context, name string) (bool, error) {
	body, _, err := rawGet(ctx, "/apis/apiregistration.k8s.io/v1/apiservices/"+name)
	if err != nil {
		return false, nil
	}
	var as struct {
		Status struct {
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &as); err != nil {
		return false, nil
	}
	for _, c := range as.Status.Conditions {
		if c.Type == "Available" {
			return c.Status == "True", nil
		}
	}
	return false, nil
}

// --- custom metrics API helpers ---

// advertisedMetrics returns the set of custom metric names the adapter currently
// advertises, derived from the API discovery list (resource names look like
// "pods/<metric>" or "namespaces/<metric>").
func advertisedMetrics(ctx context.Context, t *testing.T) map[string]struct{} {
	t.Helper()
	body, code, err := rawGet(ctx, customMetricsBase)
	if err != nil || code != http.StatusOK {
		// Discovery can briefly 503 while aggregation warms up; treat as empty.
		return map[string]struct{}{}
	}
	var list metav1.APIResourceList
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decoding custom metrics discovery: %v", err)
	}
	out := make(map[string]struct{}, len(list.APIResources))
	for _, r := range list.APIResources {
		if _, metric, ok := strings.Cut(r.Name, "/"); ok {
			out[metric] = struct{}{}
		}
	}
	return out
}

func isAdvertised(ctx context.Context, t *testing.T, metric string) bool {
	_, ok := advertisedMetrics(ctx, t)[metric]
	return ok
}

// metricValue is a minimal view of custom_metrics.MetricValueList.
type metricValueList struct {
	Items []struct {
		Metric struct {
			Name string `json:"name"`
		} `json:"metric"`
		Value string `json:"value"`
	} `json:"items"`
}

func getPodMetric(ctx context.Context, t *testing.T, namespace, metric string) (metricValueList, int) {
	t.Helper()
	path := fmt.Sprintf("%s/namespaces/%s/pods/*/%s", customMetricsBase, namespace, metric)
	body, code, err := rawGet(ctx, path)
	if err != nil && code == 0 {
		t.Fatalf("querying metric %s: %v", metric, err)
	}
	var out metricValueList
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decoding metric value list: %v", err)
		}
	}
	return out, code
}

// rawGet performs a GET against an absolute apiserver path, returning the body
// and HTTP status code.
func rawGet(ctx context.Context, path string) ([]byte, int, error) {
	res := restClient.Get().AbsPath(path).Do(ctx)
	var code int
	res.StatusCode(&code)
	body, err := res.Raw()
	return body, code, err
}

// --- mock Elasticsearch control plane ---

func mockReset(t *testing.T) {
	t.Helper()
	mockPost(t, "/__reset", nil)
}

func mockControl(t *testing.T, payload any) {
	t.Helper()
	mockPost(t, "/__control", payload)
}

// mockAddKnown registers fields the mock should report as numeric (and serve
// values for), without disturbing fields other tests rely on.
func mockAddKnown(t *testing.T, fields ...string) {
	t.Helper()
	mockControl(t, map[string]any{"addKnown": fields})
}

type mockRequest struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Query  string `json:"query"`
	Body   string `json:"body"`
}

func mockRequests(t *testing.T) []mockRequest {
	t.Helper()
	resp, err := http.Get(mockURL + "/__requests")
	if err != nil {
		t.Fatalf("mock /__requests: %v", err)
	}
	defer resp.Body.Close()
	var out []mockRequest
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding mock requests: %v", err)
	}
	return out
}

func mockPost(t *testing.T, path string, payload any) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshaling mock payload: %v", err)
		}
		body = bytes.NewReader(b)
	}
	resp, err := http.Post(mockURL+path, "application/json", body)
	if err != nil {
		t.Fatalf("mock POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mock POST %s: status %d", path, resp.StatusCode)
	}
}

// --- HPA helpers ---

// createPodsHPA creates an HPA with a single Pods custom-metric and registers a
// cleanup to delete it. The scaleTargetRef need not exist — the adapter only
// reads spec.metrics.
func createPodsHPA(ctx context.Context, t *testing.T, namespace, name, metric string) {
	t.Helper()
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MinReplicas: ptr.To(int32(1)),
			MaxReplicas: 1,
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1", Kind: "Deployment", Name: "nonexistent",
			},
			Metrics: []autoscalingv2.MetricSpec{{
				Type: autoscalingv2.PodsMetricSourceType,
				Pods: &autoscalingv2.PodsMetricSource{
					Metric: autoscalingv2.MetricIdentifier{Name: metric},
					Target: autoscalingv2.MetricTarget{
						Type:         autoscalingv2.AverageValueMetricType,
						AverageValue: resource.NewQuantity(42, resource.DecimalSI),
					},
				},
			}},
		},
	}
	_, err := clientset.AutoscalingV2().HorizontalPodAutoscalers(namespace).Create(ctx, hpa, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("creating HPA %s/%s: %v", namespace, name, err)
	}
	t.Cleanup(func() {
		_ = clientset.AutoscalingV2().HorizontalPodAutoscalers(namespace).
			Delete(context.Background(), name, metav1.DeleteOptions{})
	})
}

func deleteHPA(ctx context.Context, t *testing.T, namespace, name string) {
	t.Helper()
	if err := clientset.AutoscalingV2().HorizontalPodAutoscalers(namespace).
		Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("deleting HPA %s/%s: %v", namespace, name, err)
	}
}

// bumpHPA mutates an annotation to force an informer Update event, which drives
// the watcher's retry of any transiently-unresolved metrics.
func bumpHPA(ctx context.Context, t *testing.T, namespace, name string) {
	t.Helper()
	patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"e2e-bump":"%d"}}}`, time.Now().UnixNano()))
	if _, err := clientset.AutoscalingV2().HorizontalPodAutoscalers(namespace).
		Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		t.Fatalf("patching HPA %s/%s: %v", namespace, name, err)
	}
}

// fieldCapsAttempts counts recorded _field_caps requests that probe the given
// metric (its name appears in the fields= query parameter).
func fieldCapsAttempts(t *testing.T, metric string) int {
	t.Helper()
	n := 0
	for _, r := range mockRequests(t) {
		if strings.HasSuffix(r.Path, "/_field_caps") && strings.Contains(r.Query, metric) {
			n++
		}
	}
	return n
}

// --- generic helpers ---

// consistently asserts cond stays true for the whole duration.
func consistently(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if !cond() {
			t.Fatalf("condition became false within %s", d)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// eventually polls cond until it returns true or the timeout elapses.
func eventually(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func pollUntil(ctx context.Context, interval time.Duration, cond func() (bool, error)) error {
	for {
		ok, err := cond()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
