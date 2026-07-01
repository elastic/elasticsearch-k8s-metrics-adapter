# Integration / e2e tests

End-to-end tests for the adapter's **hpa discovery mode**, run against a local
[kind](https://kind.sigs.k8s.io/) cluster with a mock Elasticsearch backend.

The full plan (scenarios, phasing, rationale) lives in [plan.md](plan.md).
This is **Phase 0**: scaffolding plus a smoke test.

## Prerequisites

`docker`, `kind`, `kubectl`, `helm`, and `go` on the `PATH`.

## Layout

```
it/
├── plan.md                 # the full test plan
├── kind-config.yaml        # 1-node cluster; maps mock ES NodePort 30080 to localhost
├── mockes/                 # mock Elasticsearch (stdlib-only Go HTTP server)
├── testdata/
│   ├── values-e2e.yaml         # Helm values: hpa mode, mock ES host, no external secret
│   ├── mockes.yaml             # mock ES Deployment + NodePort Service
│   ├── externalsecret-crd-stub.yaml  # lets the chart's ExternalSecret apply inertly
│   └── hpa-startup.yaml         # pre-existing HPA (scenario 1)
└── e2e/                    # //go:build e2e Go suite (client-go + testify)
```

## Running

```sh
make e2e-up     # create cluster, build+load images, install mock ES + adapter (hpa mode)
make e2e        # run the suite (go test -tags e2e ./it/e2e/...)
make e2e-down   # tear down the cluster
```

`e2e-up` applies `hpa-startup.yaml` and waits for the mock to be Ready **before**
installing the adapter, so the "advertise a pre-existing HPA on startup" scenario
is exercised deterministically.

## How it fits together

- The adapter is installed via the production Helm chart with
  `it/testdata/values-e2e.yaml` (`discoveryMode: hpa`, ES host pointed at the
  in-cluster `mock-elasticsearch` Service, `envFrom: []`).
- **mockes** is a controllable fake Elasticsearch. It serves `_field_caps` and
  `_search` (never `_mapping`), and exposes a control plane:
  - `POST /__control` — e.g. `{"setKnown":["a.b.c"]}` or
    `{"failNext":{"path":"_field_caps","times":1,"status":500}}`
  - `GET /__requests` — the recorded request log (used to prove no `_mapping`)
  - `POST /__reset` — clear log + injected failures
  These are reachable from the host at `http://localhost:30080` via the kind port
  mapping.
- The Go suite talks to the cluster through the current kubeconfig context (it
  refuses to run unless the context is `kind-*`; override with
  `E2E_SKIP_CONTEXT_GUARD=1`). It queries the aggregated
  `custom.metrics.k8s.io/v1beta2` API to assert advertisement and values.

## Scenarios

| Test | Scenario |
|---|---|
| `TestAdapterReadyAndAggregated` | adapter Ready + aggregated API serving |
| `TestAdvertiseExistingHPAOnStartup` | pre-existing HPA's metric advertised + value fetchable |
| `TestAdvertiseOnHPACreate` | HPA created at runtime → metric advertised |
| `TestWithdrawOnLastHPADelete` | shared metric withdrawn only when the last HPA is deleted |
| `TestUnadvertisedMetricNotFound` | unreferenced metric is unadvertised and 404s |
| `TestNoMappingCalls` | discovery uses `_field_caps`, never `_mapping` |
| `TestValueQueryShape` | value fetch filters by field + namespace + pod |
| `TestStaticSearchFieldServed` | static search field served without `_field_caps` |
| `TestTransientFailureIsRetried` | transiently-failed resolve is retried |

## Notes

- Images use the `:e2e` tag and are `kind load`-ed; the default
  `imagePullPolicy` (`IfNotPresent` for non-`latest` tags) uses the preloaded
  images, so no registry access is needed.
- `E2E_ARCH` defaults to the host arch (`go env GOARCH`), which matches the kind
  node arch. Override if cross-building.
