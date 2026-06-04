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

package registry

import (
	"context"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/sync/singleflight"

	"sigs.k8s.io/custom-metrics-apiserver/pkg/provider"

	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/client"
	"github.com/elastic/elasticsearch-k8s-metrics-adapter/pkg/log"
)

// DefaultNegativeCacheTTL is how long an unresolved metric stays "known missing".
// Short enough that newly-indexed fields are picked up quickly, long enough that
// a misconfigured HPA polling every reconcile doesn't hammer ES.
const DefaultNegativeCacheTTL = 1 * time.Minute

// ResolvedEntry is what the resolver hands back to the registry on a successful
// resolution: the canonical CustomMetricInfo (post-rename) and the client that
// will serve subsequent value queries.
type ResolvedEntry struct {
	Info   provider.CustomMetricInfo
	Client client.Interface
}

// Resolver looks up which client serves a given custom metric name. It caches
// both positive and negative results, and uses singleflight to dedupe concurrent
// lookups for the same name.
type Resolver struct {
	logger      logr.Logger
	clients     []client.Interface
	negativeTTL time.Duration

	positive sync.Map // string → *ResolvedEntry
	negative sync.Map // string → time.Time (expiry)

	sf singleflight.Group

	// nowFn is overridable in tests.
	nowFn func() time.Time
}

// NewResolver builds a Resolver over the given clients. Clients are tried in
// the order provided; the first one to report the metric as served wins.
func NewResolver(clients []client.Interface, negativeTTL time.Duration) *Resolver {
	if negativeTTL <= 0 {
		negativeTTL = DefaultNegativeCacheTTL
	}
	return &Resolver{
		logger:      log.ForPackage("resolver"),
		clients:     clients,
		negativeTTL: negativeTTL,
		nowFn:       time.Now,
	}
}

// Resolve returns the entry for the given metric name, or nil if no configured
// client serves it. A non-nil error means the lookup itself failed transiently
// (the caller should not cache the result).
func (r *Resolver) Resolve(ctx context.Context, metricName string) (*ResolvedEntry, error) {
	if entry, ok := r.lookupPositive(metricName); ok {
		return entry, nil
	}
	if r.lookupNegative(metricName) {
		return nil, nil
	}

	v, err, _ := r.sf.Do(metricName, func() (interface{}, error) {
		// Re-check the caches inside the singleflight to absorb any racers
		// that got past the initial check.
		if entry, ok := r.lookupPositive(metricName); ok {
			return entry, nil
		}
		if r.lookupNegative(metricName) {
			return (*ResolvedEntry)(nil), nil
		}

		for _, c := range r.clients {
			info, found, err := c.ResolveCustomMetric(ctx, metricName)
			if err != nil {
				// Transient: do not cache. Surface the error to the caller.
				r.logger.V(1).Info("resolve failed",
					"metric", metricName,
					"client", c.GetConfiguration().Name,
					"err", err.Error())
				return nil, err
			}
			if found {
				entry := &ResolvedEntry{Info: info, Client: c}
				r.positive.Store(metricName, entry)
				return entry, nil
			}
		}
		r.negative.Store(metricName, r.nowFn().Add(r.negativeTTL))
		return (*ResolvedEntry)(nil), nil
	})
	if err != nil {
		return nil, err
	}
	entry, _ := v.(*ResolvedEntry)
	return entry, nil
}

func (r *Resolver) lookupPositive(metricName string) (*ResolvedEntry, bool) {
	v, ok := r.positive.Load(metricName)
	if !ok {
		return nil, false
	}
	return v.(*ResolvedEntry), true
}

func (r *Resolver) lookupNegative(metricName string) bool {
	v, ok := r.negative.Load(metricName)
	if !ok {
		return false
	}
	expiry := v.(time.Time)
	if r.nowFn().After(expiry) {
		r.negative.Delete(metricName)
		return false
	}
	return true
}
