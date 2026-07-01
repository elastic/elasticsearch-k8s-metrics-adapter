# HPA-driven discovery mode

This document explains how the adapter serves custom metrics when started with
`--discovery-mode=hpa`, and how the main components collaborate.

## Why this mode exists

The adapter implements the Kubernetes [custom metrics API](https://github.com/kubernetes/metrics)
so an HPA can scale a workload on a metric stored in Elasticsearch.

The original (`periodic`) mode discovered metrics by fetching the **entire**
`_mapping` of each configured index pattern every minute and decoding it into
`map[string]interface{}`. For large indices this can require a lot of memory,
repeated every minute. The scan is also wasteful: it advertises every numeric
field so that a handful of consumers can read a few.

`hpa` mode inverts the model. Instead of discovering *everything* and hoping
someone uses it, it observes **which metrics are actually referenced by HPAs**
and resolves only those, each with a targeted `_field_caps` call (server-side
filtered to numeric types, with the bulky `indices` section stripped from the
response). Memory pressure drops from "the whole mapping every minute" to "one
tiny lookup per distinct metric, once".

A subtlety drives the whole design: the Kubernetes API server **404s a request
for a metric the adapter has not advertised**, before the request ever reaches
us. So lazily resolving on first request is not enough — the metric must be
*advertised* (present in `ListAllCustomMetrics`) ahead of time. Watching HPAs is
how we know what to advertise.

## Components at a glance

| Component | Package | Responsibility |
|---|---|---|
| `ElasticsearchAdapter` | `main` | Process entry point. Parses flags, builds clients, wires everything, starts the HPA watcher + scheduler + API server. |
| `Watcher` | `pkg/hpa` | Watches `HorizontalPodAutoscaler` objects via an informer; turns HPA changes into `Advertise`/`Withdraw` calls. |
| `referenceTracker` | `pkg/hpa` | Ref-counts which metric names are referenced by which HPAs; reports the first add and last removal of each name. |
| `Registry` | `pkg/registry` | Central routing + advertisement table. Resolves a name to a client (`Advertise`), lists advertised metrics, routes value requests (`GetCustomMetricClient`). |
| `elasticsearch.MetricsClient` | `pkg/client/elasticsearch` | Talks to ES. Resolves a metric via `_field_caps` (`ResolveCustomMetric`) and fetches values (`GetMetricByName`/`BySelector`). |
| `recorder` | `pkg/client/elasticsearch` | Builder that accumulates `{metrics, indexedMetrics}` while discovering. Seeds static (search-based) fields at client construction. |
| `custom_api.metricsClient` | `pkg/client/custom_api` | Non-ES backend. Still uses periodic discovery; its list endpoint is cheap. |
| `aggregationProvider` | `pkg/provider` | Implements the custom-metrics-apiserver provider interface; the bridge from the K8s API to the `Registry`. |
| monitoring server | `pkg/monitoring` | `/readyz` + Prometheus metrics. |
| scheduler | `pkg/scheduler` | Periodic discovery loop, used only for non-ES clients in this mode. |

> **Resolution.** There is no separate "resolver" object: the `Registry` holds
> the list of *resolver clients* and its `Advertise` method resolves a metric by
> walking them. When this doc says "the registry resolves a metric", that is what
> it means.

## Structure (class diagram)

```mermaid
classDiagram
    class ElasticsearchAdapter {
        +DiscoveryMode string
        +startHPAWatcher(registry)
    }

    class Watcher {
        -registry MetricRegistry
        -tracker referenceTracker
        -informer SharedIndexInformer
        -unresolved set_of_names
        +Start(ctx) error
        -onUpsert(obj)
        -onDelete(obj)
        -advertiseOne(name)
        -retryUnresolved()
    }

    class referenceTracker {
        -byHPA hpaKey_to_nameSet
        -refCount name_to_count
        +upsert(key, names) added_removed
        +remove(key) removed
    }

    class MetricRegistry {
        <<interface>>
        +Advertise(ctx, name) (bool, error)
        +Withdraw(name)
    }

    class Registry {
        -customMetrics info_to_clients
        -advertisedByName name_to_info
        -resolverClients clientList
        +WithResolverClients(clients) Registry
        +Advertise(ctx, name) bool_error
        +Withdraw(name)
        +GetCustomMetricClient(info) client
        +ListAllCustomMetrics() infoList
        +UpdateCustomMetrics(client, set)
    }

    class clientInterface {
        <<interface>>
        +ResolveCustomMetric(ctx, name) info_bool_error
        +GetMetricByName(...) MetricValue
        +GetMetricBySelector(...) MetricValueList
        +ListCustomMetricInfos() set
    }

    class ElasticsearchMetricsClient {
        -metrics name_to_info
        -indexedMetrics name_to_metadata
        -namer Namer
        +ResolveCustomMetric(ctx, name)
        +GetMetricByName(...)
        -fieldExistsAsNumeric(...) bool
    }

    class recorder {
        -metrics map
        -indexedMetrics map
        +recordStaticFields(cfg)
        +processMappingDocument(...)
    }

    class aggregationProvider {
        -registry Registry
        +GetMetricByName(...)
        +ListAllCustomMetrics()
    }

    ElasticsearchAdapter --> Watcher : starts (hpa mode)
    ElasticsearchAdapter --> Registry : builds + wires
    ElasticsearchAdapter --> aggregationProvider : serves via API server
    Watcher --> referenceTracker : diffs HPA refs
    Watcher ..> MetricRegistry : Advertise / Withdraw
    Registry ..|> MetricRegistry : implements
    aggregationProvider --> Registry : routes requests
    Registry --> clientInterface : resolverClients + routing
    ElasticsearchMetricsClient ..|> clientInterface : implements
    ElasticsearchMetricsClient --> recorder : seeds static fields at construction
```

Key relationships:

- The `Watcher` depends only on the small `MetricRegistry` interface
  (`Advertise`/`Withdraw`), not the concrete `Registry` — easy to test with a fake.
- The `Registry` depends on `client.Interface`, so it treats Elasticsearch and
  custom-api backends uniformly.
- `customMetrics` is simultaneously the **advertisement catalogue** (what
  `ListAllCustomMetrics` returns) and the **routing table** (what
  `GetCustomMetricClient` looks up). `advertisedByName` is a secondary index so a
  metric can be withdrawn by its plain name.

## Startup (sequence diagram)

```mermaid
sequenceDiagram
    participant Main as ElasticsearchAdapter (main)
    participant ES as ElasticsearchMetricsClient
    participant Reg as Registry
    participant W as Watcher
    participant Inf as HPA Informer
    participant API as custom-metrics API server

    Main->>ES: newMetricsClients()
    activate ES
    ES->>ES: recordStaticFields() — seed search-based metrics
    deactivate ES
    Main->>Main: split clients → resolverClients (ES) / scheduledClients (custom_api)
    Main->>Reg: NewRegistry().WithResolverClients(resolverClients)
    Main->>Reg: seed monitoring readiness (empty update per resolver client)
    Main->>W: startHPAWatcher(registry)
    activate W
    W->>Inf: factory.Start() + WaitForCacheSync (blocks)
    Inf-->>W: AddFunc for each existing HPA
    loop per existing HPA
        W->>Reg: Advertise(name) for newly-referenced names
    end
    W-->>Main: cache synced (registry warm)
    deactivate W
    Main->>API: start scheduler (scheduledClients) + serve aggregationProvider
    Note over Main,API: Registry already lists the HPA-referenced metrics,<br/>so the API server will route requests for them.
```

The watcher's `Start` **blocks on cache sync** on purpose: by the time the API
server begins serving, every metric referenced by an already-existing HPA is
advertised, so the very first scrape is not a 404.

## HPA event flow (sequence diagram)

What happens when an HPA is created, updated, or deleted:

```mermaid
sequenceDiagram
    participant Inf as HPA Informer
    participant W as Watcher
    participant T as referenceTracker
    participant Reg as Registry
    participant ES as ElasticsearchMetricsClient
    participant ESsrv as Elasticsearch

    Inf->>W: onUpsert(hpa) on Add, Update or resync
    W->>W: metricNames(hpa), Pods and Object metrics only
    W->>T: upsert(key, names)
    T-->>W: added and removed names
    loop name in added
        W->>Reg: Advertise(name)
        Reg->>ES: ResolveCustomMetric(name)
        alt fast path, already known incl. static fields
            ES-->>Reg: info, found
        else probe Elasticsearch
            ES->>ESsrv: field_caps, single field, numeric types, fields-only payload
            ESsrv-->>ES: field types, tiny payload
            ES->>ES: register in metrics and indexedMetrics
            ES-->>Reg: info found, or not found
        end
        alt found
            Reg->>Reg: add to customMetrics and advertisedByName
        else transient error
            W->>W: mark name unresolved for retry
        end
    end
    loop name in removed
        W->>Reg: Withdraw(name)
        Reg->>Reg: delete from customMetrics and advertisedByName
    end
    W->>W: retryUnresolved, re-Advertise names that errored earlier
```

Notes:

- `metricNames` only extracts **Pods** and **Object** metric types — those are
  the ones served via the custom metrics API. `Resource`/`ContainerResource`
  (cpu/memory) and `External` metrics go through other APIs and are ignored.
- The `referenceTracker` ensures a name is advertised when the **first** HPA
  references it and withdrawn when the **last** one stops — `Advertise` is never
  called redundantly for an already-tracked name, which is why the registry
  needs no per-name resolution cache.
- `retryUnresolved` exists because the tracker reports each name as `added` only
  once. If that single `Advertise` hit a transient ES error, the name would
  otherwise never be retried. Names that errored are kept in an `unresolved` set
  and re-attempted on every later HPA event (status updates and the informer's
  10-minute resync both re-deliver objects). It is a no-op in steady state.

## Serving a metric value (sequence diagram)

Once a metric is advertised, this is what a scaling query looks like:

```mermaid
sequenceDiagram
    participant K8s as kube-apiserver (HPA controller)
    participant API as adapter API server
    participant P as aggregationProvider
    participant Reg as Registry
    participant ES as ElasticsearchMetricsClient
    participant ESsrv as Elasticsearch

    K8s->>API: GET custom metric value for the metric
    API->>P: GetMetricByName(name, info, selector)
    P->>Reg: GetCustomMetricClient(info)
    Reg-->>P: ES client, or 404 if not advertised
    P->>ES: GetMetricByName(...)
    ES->>ES: namer.Get maps alias to real field, look up indexedMetrics
    ES->>ESsrv: search query, exists plus namespace plus pod, latest value
    ESsrv-->>ES: value and timestamp
    ES-->>P: MetricValue
    P-->>API: MetricValue
    API-->>K8s: MetricValue
```

The value path uses the `indexedMetrics` metadata (index pattern + field, or a
search body for static fields) that was populated during `ResolveCustomMetric` /
`recordStaticFields`. Listing (`ListAllCustomMetrics`) takes the same provider →
registry hop but just enumerates `customMetrics`.

## Lifecycle of a single metric (state diagram)

```mermaid
stateDiagram-v2
    [*] --> Unknown : not referenced by any HPA

    Unknown --> Advertised : HPA references it<br/>Advertise → found
    Unknown --> NotServed : HPA references it<br/>Advertise → not found
    Unknown --> Unresolved : HPA references it<br/>Advertise → transient error

    Unresolved --> Advertised : retry → found
    Unresolved --> NotServed : retry → not found
    Unresolved --> Unknown : HPA stops referencing it

    NotServed --> Advertised : field later appears in ES<br/>(on next HPA add of the name)
    NotServed --> Unknown : HPA stops referencing it

    Advertised --> Unknown : last HPA reference removed<br/>Withdraw

    note right of Advertised
        Listed by ListAllCustomMetrics
        Routable by GetCustomMetricClient
        Cached in the ES client's
        metrics / indexedMetrics maps
    end note

    note right of Unresolved
        Held in Watcher.unresolved,
        re-Advertised on every
        subsequent HPA event or resync
    end note
```

- **Advertised** is the only state visible to Kubernetes. The metric is in
  `customMetrics` (so it is listed and routable) and `advertisedByName` (so it
  can be withdrawn by name).
- **NotServed** means the registry returned `found=false`. The watcher does not
  retry it (no `unresolved` entry); it is reconsidered only when the name is
  added again as a *fresh* reference (its ref-count goes 0 → 1 again), which
  re-probes Elasticsearch. There is no negative cache, so the cost of a
  not-served name is exactly one `_field_caps` call per fresh reference.
- **Withdraw** removes the metric from the registry but **not** from the ES
  client's internal maps. That is deliberate: if an HPA references it again,
  `ResolveCustomMetric`'s fast path returns the cached metadata with no ES call.
  The set of distinct HPA-referenced metrics is small, so this cache is bounded.

## `periodic` vs `hpa` at a glance

| | `periodic` | `hpa` |
|---|---|---|
| ES discovery | full `_mapping` scan every minute | per-metric `_field_caps`, on demand |
| What's advertised | every numeric field in the index | only metrics referenced by an HPA |
| Trigger | scheduler tick | HPA add/update/delete events |
| Registry populated by | `UpdateCustomMetrics` (scheduler) | `Advertise`/`Withdraw` (watcher) |
| Static (search) fields | recorded each discovery cycle | seeded once at client construction |
| Memory profile | high, grows with index size | low, bounded by HPA count |
| RBAC | — | `get`/`list`/`watch` on `horizontalpodautoscalers` |

In `hpa` mode, **only Elasticsearch clients** take the resolver path; other
client types (e.g. custom-api) keep going through the periodic scheduler because
their list endpoints are cheap.

## Known limitations

`hpa` mode is a deliberate trade-off against `periodic`. `periodic` remains the
default; switch to `hpa` only if the points below are acceptable for your
deployment.

### The discovery catalogue lists only demanded metrics

In `hpa` mode `ListAllCustomMetrics` (`GET /apis/custom.metrics.k8s.io/v1beta1/`,
`kubectl get --raw`, dashboards) lists **only** the metrics currently referenced
by a live HPA, and the set changes as HPAs come and go. Autoscaling itself is
unaffected — an HPA names the metric it needs — but you can no longer enumerate
every metric available to scale on. This is an accepted, deliberate loss of
enumeration. Use `periodic` if you rely on that enumeration.

### Cold-start window for newly-referenced metrics

A metric referenced by an HPA created *after* startup returns `404`
("unable to fetch metrics") until the watcher resolves and advertises it. HPAs
already present at startup are unaffected: `Watcher.Start` blocks on the initial
informer cache sync, so every already-referenced metric is advertised before the
API server begins serving.

### `metricSets` is still required and still scopes resolution

`metricSets` is not optional in `hpa` mode. Validation rejects an Elasticsearch
server with no `metricSets` in every mode, and the configured `metricSets` still
supply both the `_field_caps` target indices and the field-pattern allow-list
used to accept or reject a referenced metric name.

### Object-type metrics are not served

HPA `Object` metrics are ignored by the watcher. The ES client advertises every
resolved metric under the `pods` resource, so an `Object` metric on a non-pod
resource would be advertised as `pods/<name>` and never match. Rather than
advertise metrics it cannot serve correctly, `hpa` mode skips them until the
resource can be inferred from configuration. Use `Pods` metrics.

### The client-side metric cache only grows

`Withdraw` removes a metric from the registry but not from the ES client's
internal `metrics`/`indexedMetrics` cache. A re-referenced metric is then served
from that cached positive without re-validating against Elasticsearch. The cache
is bounded by the small set of distinct HPA-referenced names, so this is not a
growth concern.

### `rename` is not supported in `hpa` mode

The `rename` config directive (`matches` / `as`) lets you expose ES fields under
a different name to Kubernetes. It works in `periodic` mode because
`discoverMetrics()` walks every field and calls `namer.Register(realFieldName)`
for each one, building the alias → real-name map before any HPA query arrives.

In `hpa` mode `discoverMetrics()` never runs, so the map is never populated.
When `ResolveCustomMetric` receives the alias from the HPA it probes `_field_caps`
with the alias name — which doesn't exist in Elasticsearch — and silently returns
not-found. The metric is never advertised.

If you rely on `rename`, use `--discovery-mode=periodic` for now.
