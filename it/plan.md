# E2E test plan — `hpa` discovery mode

Validates the `--discovery-mode=hpa` behavior introduced on the
`lazy-field-caps-discovery` branch (fix for
[ecp-services-team#1959](https://github.com/elastic/ecp-services-team/issues/1959):
the adapter is OOMKilled because periodic mode decodes the full `_mapping`
every minute). The new mode watches HPAs and resolves only referenced metrics
via `_field_caps`.

## Decisions (settled)

- **Backend: mock Elasticsearch** (a small Go HTTP fake we control), not a real
  ES. It gives deterministic control real ES can't: inject a 500 on demand,
  assert exactly which endpoints were hit, and serve canned payloads.
- **Driver: Go**, a `//go:build e2e`-tagged suite using `client-go` + `testify`.
- **Cluster: kind**, created from `it/kind-config.yaml`.
- **CI: out of scope here** — wiring the Buildkite step is handled separately.

## 1. What we're validating, and what's already covered

Unit tests already cover the components in isolation:

- `referenceTracker` ref-counting, `metricNames` extraction — `pkg/hpa/tracker_test.go`
- `Watcher` event → advertise/withdraw + transient retry — `pkg/hpa/watcher_test.go`
- `Registry.Advertise` / `Withdraw` resolution — `pkg/registry/advertise_test.go`

E2E therefore targets **only the seams unit tests can't reach**:

1. Real APIServer **aggregation**: a metric is 404 until advertised, then
   routable — the core premise of the design.
2. Real **informer** events end-to-end (HPA create / update / delete → catalogue
   changes).
3. The adapter actually emits the expected **`_field_caps` / `_search`** calls
   and **never calls `_mapping`** in hpa mode (the OOM fix, made observable).
4. **Helm + RBAC** wiring: the watch-HPA ClusterRole rule, `discoveryMode: hpa`,
   readiness.
5. Our two recent fixes under a real cluster: **static search fields** (Issue B)
   and **transient-failure retry** (Issue A).

## 2. Test environment topology (all inside kind)

```
kind cluster
├── kube-apiserver ──(aggregates)──► APIService v1beta1/v1beta2.custom.metrics.k8s.io
├── ns metrics-adapter
│   ├── elasticsearch-k8s-metrics-adapter   (image kind-loaded, helm-installed, discoveryMode=hpa)
│   └── mock-elasticsearch  (Deployment + Service; our controllable fake ES)
├── ns workloads
│   └── HPAs + dummy scale targets   (test fixtures)
└── Go test driver (runs on host, talks to cluster via KUBECONFIG)
```

## 3. Mock Elasticsearch design (`it/mockes/`)

A small Go HTTP server with its own tiny image, kind-loaded like the adapter.

| Endpoint | Behavior |
|---|---|
| `GET /` | ES version banner (the go-elasticsearch client pings on init). |
| `GET /{index}/_field_caps` | Returns numeric field types from a loaded fixture; honors `fields=`, `types=`, `filter_path=fields` so the test can assert the adapter sends them. |
| `POST /{index}/_search` | Returns a canned hit `{value, @timestamp, kubernetes.*}` for known fields. |
| `POST /__control` | Test hook, e.g. `{"failNext":{"path":"_field_caps","times":1,"status":500}}`, or swap the active fixture. |
| `GET /__requests` | Returns the recorded request log (method, path, query, body) for assertions. |
| `POST /__reset` | Clears control state + request log (called between tests for isolation). |

The request log is what makes scenarios 3 and 9 below provable. There is
deliberately **no `_mapping` handler** — scenario 3 asserts the adapter never
calls it.

## 4. Scenario matrix

| # | Scenario | Steps | Assertion | Exercises |
|---|---|---|---|---|
| 1 | **Advertise existing HPA on startup** | HPA exists before adapter ready | metric listed in `/apis/custom.metrics.k8s.io/v1beta2` and value fetchable | watcher cache-sync blocking + `Advertise` |
| 2 | **Advertise on HPA create** | create HPA after adapter ready | metric advertised within ~10s | informer AddFunc |
| 3 | **No `_mapping` ever called** | run any of the above | `/__requests` has `_field_caps`/`_search`, **zero** `_mapping` | the OOM fix premise |
| 4 | **Withdraw on last HPA delete** | 2 HPAs share metric M; delete one, then the other | M stays advertised after 1st delete, gone after 2nd | `referenceTracker` ref-count |
| 5 | **Unadvertised metric is 404** | query a metric no HPA references | aggregated API returns 404 | "must advertise first" routing |
| 6 | **Not-served metric** | HPA references a field the mock doesn't know | metric **not** advertised, adapter healthy, log line emitted | `Advertise` → not found |
| 7 | **Static search field (Issue B)** | helm config adds a `fields:[{name,search}]` metricSet; HPA references it | advertised + value served, though `_field_caps` wouldn't find it | `recordStaticFields` in hpa mode |
| 8 | **Transient retry (Issue A)** | `__control` fails first `_field_caps` with 500; create HPA | initially not advertised; becomes advertised after retry (HPA touch / resync) | `Watcher.unresolved` retry |
| 9 | **Value query shape** | fetch a metric value | mock received `_search` with `exists` + namespace + pod filters | ES client `valueFor` |
| 10 | **Readiness** | after install | `/readyz` 200; pod Ready | resolver-client readiness seeding |
| 11 | **RBAC present** | `helm template` / live SelfSubjectAccessReview | ClusterRole grants `watch horizontalpodautoscalers` | `helm/templates/cluster-role.yaml` |

## 5. Repo layout & tooling

```
it/
├── plan.md                   # this document
├── README.md                 # how to run (short; points here)
├── kind-config.yaml          # promoted from repo root
├── mockes/                   # mock Elasticsearch
│   ├── main.go
│   ├── Dockerfile
│   └── fixtures/             # canned _field_caps / _search payloads
├── e2e/
│   ├── suite_test.go         # //go:build e2e — bootstrap, preconditions, TestMain
│   ├── helpers.go            # waitFor(), listCustomMetrics(), getMetricValue(), applyHPA(), mockControl(), mockRequests()
│   ├── advertise_test.go     # scenarios 1, 2, 4, 5
│   ├── observability_test.go # scenarios 3, 9
│   ├── static_field_test.go  # scenario 7
│   └── retry_test.go         # scenarios 6, 8
└── testdata/
    ├── values-e2e.yaml       # discoveryMode: hpa, host = mock-es svc, dummy creds, + static-field metricSet
    ├── hpa-*.yaml
    └── fieldcaps-*.json      # mock ES fixtures
```

- **Driver**: plain `client-go` + `testify` behind `//go:build e2e` (no heavy
  framework; matches the repo's Go-test style). Custom-metrics queries hit
  `GET /apis/custom.metrics.k8s.io/...` via the REST client — the same calls the
  existing `test-advertise/test.sh` makes with `curl`, but with assertions and
  polling.
- **Make targets** (replacing the stale `test-kind`, which points at a
  non-existent `deploy/…yaml`):
  - `make e2e-up` — `kind create cluster --config it/kind-config.yaml`; build +
    `kind load` the adapter and mockes images; `helm install -f
    it/testdata/values-e2e.yaml`; wait for the APIService to be `Available`.
  - `make e2e` — `go test -tags e2e -count=1 ./it/e2e/...`
  - `make e2e-down` — `kind delete cluster`

## 6. Setup / teardown contract

`suite_test.go` `TestMain`:

- Guard: refuse to run unless the current context is a kind cluster (avoid
  touching a real cluster).
- Wait for adapter + mockes Deployments Ready and the APIService
  `Available=True` before any test runs.
- Reset mock state (`__reset`) between tests for isolation.
- Each test creates its HPAs in a fresh namespace and cleans up after itself.

## 7. Phasing (incremental, each independently mergeable)

1. **Phase 0 — scaffolding**: `it/kind-config.yaml`, `mockes/` (just `/`,
   `_field_caps`, `_search`), `values-e2e.yaml`, Make targets, suite bootstrap.
   Land scenarios 1 + 10 as the smoke test.
2. **Phase 1 — core behavior**: scenarios 2, 4, 5 (advertise / withdraw / 404)
   plus request-log assertions (3, 9).
3. **Phase 2 — our fixes**: scenarios 7 (static fields) and 8 (retry) plus the
   `__control` failure-injection path in mockes.

## 8. Notes / risks

- **Image loading**: the adapter and mockes images must be `kind load`-ed; pin
  `imagePullPolicy: IfNotPresent` (or `Never`) in `values-e2e.yaml` so kind
  doesn't try to pull from a registry.
- **APIService readiness** is the most common flake source: always gate tests on
  `Available=True`, not just pod Ready.
- **`discoveryMode` default**: `helm/values.yaml` currently defaults to
  `full`; `values-e2e.yaml` must set `hpa` explicitly.
- **Config shape**: the ES host is injected via `${HPA_ELASTICSEARCH_HOST}`
  env expansion (see `helm/templates/config.yaml` + `envFrom`); point it at the
  in-cluster `mock-elasticsearch` Service and supply dummy credentials.
