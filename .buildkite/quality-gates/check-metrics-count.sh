#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(dirname -- "$0")

query_response=$("${SCRIPT_DIR}/execute-query.sh" "${SCRIPT_DIR}/queries/metrics_count.json" 'metrics-*:metrics-*')

failing_pods=$(jq -r '.aggregations.podNames.buckets[] |
    {
      pod_name: .key,
      metrics_count: (.latest_result.hits.hits[0]._source.prometheus.metrics_count.value | tonumber),
    } |
    select(.metrics_count <= 0) |
    [.pod_name, .metrics_count] |
    join(" ")' <<< "${query_response}")

if [ -z "${failing_pods}" ]; then
  echo "No pod instances with metrics_count for client: k8s-observability-cluster and type:custom as 0"
  exit 0
fi

echo "Detected pod instances with metrics_count for client: k8s-observability-cluster and type:custom as 0:\n$failing_pods"
exit 1