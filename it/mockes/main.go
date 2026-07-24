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

// Command mockes is a deterministic, controllable stand-in for Elasticsearch
// used by the adapter e2e tests. It implements just enough of the ES HTTP API
// for the hpa discovery path — _field_caps and _search — plus a small control
// plane (/__control, /__requests, /__reset) so tests can inject failures and
// assert exactly which endpoints the adapter called.
//
// It deliberately does NOT implement _mapping: the hpa discovery mode must never
// call it, and the request log lets a test prove that.
package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// elasticProductHeader is required on every response: the go-elasticsearch
// client rejects responses that do not carry it.
const elasticProductHeader = "X-Elastic-Product"

type requestEntry struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Query  string `json:"query"`
	Body   string `json:"body"`
}

type failRule struct {
	Times  int `json:"times"`
	Status int `json:"status"`
}

type controlRequest struct {
	// SetKnown replaces the known-fields set when non-nil.
	SetKnown *[]string `json:"setKnown"`
	// AddKnown adds to the known-fields set without clobbering the rest, so
	// concurrent tests don't disturb each other's fields.
	AddKnown *[]string `json:"addKnown"`
	// FailNext registers a transient failure for requests whose path contains
	// the given substring (e.g. "_field_caps").
	FailNext *struct {
		Path string `json:"path"`
		failRule
	} `json:"failNext"`
}

type server struct {
	mu       sync.Mutex
	known    map[string]struct{}
	requests []requestEntry
	failNext map[string]*failRule // path substring -> rule
}

func newServer(known []string) *server {
	s := &server{
		known:    make(map[string]struct{}, len(known)),
		failNext: make(map[string]*failRule),
	}
	for _, k := range known {
		if k = strings.TrimSpace(k); k != "" {
			s.known[k] = struct{}{}
		}
	}
	return s
}

func main() {
	listen := envOr("MOCKES_LISTEN", ":9200")
	known := strings.Split(envOr("MOCKES_KNOWN_FIELDS", "prometheus.proxy_open_connections.value"), ",")
	s := newServer(known)

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.route)
	log.Printf("mockes listening on %s, known fields: %v", listen, known)
	if err := http.ListenAndServe(listen, mux); err != nil {
		log.Fatal(err)
	}
}

// route dispatches on the request path. Admin endpoints are handled directly;
// everything else is recorded and treated as an Elasticsearch API call.
func (s *server) route(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(elasticProductHeader, "Elasticsearch")

	switch {
	case r.URL.Path == "/__requests":
		s.handleRequests(w)
		return
	case r.URL.Path == "/__reset":
		s.handleReset(w)
		return
	case r.URL.Path == "/__control":
		s.handleControl(w, r)
		return
	}

	body := readBody(r)
	s.record(requestEntry{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: body})

	switch {
	case strings.HasSuffix(r.URL.Path, "/_field_caps"):
		if s.maybeFail(w, "_field_caps") {
			return
		}
		s.handleFieldCaps(w, r)
	case strings.HasSuffix(r.URL.Path, "/_search"):
		if s.maybeFail(w, "_search") {
			return
		}
		s.handleSearch(w, body)
	case r.URL.Path == "/":
		writeJSON(w, http.StatusOK, map[string]any{
			"name":         "mockes",
			"cluster_name": "mockes",
			"version":      map[string]any{"number": "9.0.0"},
			"tagline":      "You Know, for Mocking",
		})
	default:
		writeJSON(w, http.StatusOK, map[string]any{})
	}
}

// handleFieldCaps returns, for each requested field that is known, a single
// numeric (long) capability entry. With filter_path=fields the real ES drops
// the top-level "indices" array; we only ever return "fields" anyway.
func (s *server) handleFieldCaps(w http.ResponseWriter, r *http.Request) {
	requested := splitComma(r.URL.Query().Get("fields"))
	fields := map[string]any{}
	s.mu.Lock()
	for _, f := range requested {
		if _, ok := s.known[f]; ok {
			fields[f] = map[string]any{
				"long": map[string]any{
					"type":           "long",
					"metadata_field": false,
					"searchable":     true,
					"aggregatable":   true,
				},
			}
		}
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"fields": fields})
}

// handleSearch returns one hit for the field referenced by the query's
// `exists` clause, with a nested _source so the adapter can read
// _source.<dotted.field>. Unknown fields yield zero hits.
func (s *server) handleSearch(w http.ResponseWriter, body string) {
	field := existsField(body)
	known := false
	if field != "" {
		s.mu.Lock()
		_, known = s.known[field]
		s.mu.Unlock()
	}

	hits := []any{}
	if known {
		source := map[string]any{"@timestamp": time.Now().UTC().Format(time.RFC3339)}
		setNested(source, field, 1)
		hits = append(hits, map[string]any{"_index": "metrics-mock", "_source": source})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hits": map[string]any{
			"total": map[string]any{"value": len(hits), "relation": "eq"},
			"hits":  hits,
		},
	})
}

// --- control plane ---

func (s *server) handleRequests(w http.ResponseWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, s.requests)
}

func (s *server) handleReset(w http.ResponseWriter) {
	s.mu.Lock()
	s.requests = nil
	s.failNext = make(map[string]*failRule)
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (s *server) handleControl(w http.ResponseWriter, r *http.Request) {
	var req controlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.SetKnown != nil {
		s.known = make(map[string]struct{}, len(*req.SetKnown))
		for _, k := range *req.SetKnown {
			s.known[k] = struct{}{}
		}
	}
	if req.AddKnown != nil {
		for _, k := range *req.AddKnown {
			s.known[k] = struct{}{}
		}
	}
	if req.FailNext != nil {
		rule := req.FailNext.failRule
		if rule.Status == 0 {
			rule.Status = http.StatusInternalServerError
		}
		s.failNext[req.FailNext.Path] = &rule
	}
	w.WriteHeader(http.StatusOK)
}

// maybeFail consumes a registered failure for the given path key and, if one is
// pending, writes an Elasticsearch-shaped error response and returns true.
func (s *server) maybeFail(w http.ResponseWriter, key string) bool {
	s.mu.Lock()
	rule, ok := s.failNext[key]
	if ok && rule.Times > 0 {
		rule.Times--
		status := rule.Status
		s.mu.Unlock()
		writeJSON(w, status, map[string]any{
			"error":  map[string]any{"type": "mock_injected_error", "reason": "transient failure"},
			"status": status,
		})
		return true
	}
	s.mu.Unlock()
	return false
}

func (s *server) record(e requestEntry) {
	s.mu.Lock()
	s.requests = append(s.requests, e)
	s.mu.Unlock()
}

// --- helpers ---

func existsField(body string) string {
	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return ""
	}
	query, _ := doc["query"].(map[string]any)
	boolq, _ := query["bool"].(map[string]any)
	must, _ := boolq["must"].([]any)
	for _, m := range must {
		clause, _ := m.(map[string]any)
		exists, _ := clause["exists"].(map[string]any)
		if f, ok := exists["field"].(string); ok {
			return f
		}
	}
	return ""
}

// setNested writes value at the dotted path (e.g. "a.b.c") into m, creating
// intermediate maps as needed.
func setNested(m map[string]any, dotted string, value any) {
	parts := strings.Split(dotted, ".")
	for i, p := range parts {
		if i == len(parts)-1 {
			m[p] = value
			return
		}
		next, ok := m[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[p] = next
		}
		m = next
	}
}

func splitComma(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func readBody(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
