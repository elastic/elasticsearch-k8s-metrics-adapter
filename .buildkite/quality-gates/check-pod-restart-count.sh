# Licensed to Elasticsearch B.V. under one or more contributor
# license agreements. See the NOTICE.txt file distributed with
# this work for additional information regarding copyright
# ownership. Elasticsearch B.V. licenses this file to you under
# the Apache License, Version 2.0 (the "License"); you may
# not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(dirname -- "$0")

query_response=$("${SCRIPT_DIR}/execute-query.sh" "${SCRIPT_DIR}/queries/pod_restart_count.json" 'metrics-*:metrics-*')

failing_pods=$(jq -r '.aggregations.podNames.buckets[] |
    {
      pod_name: .key,
      max_restart_count: (.max_restarts.value | try(tonumber)),
    } |
    select(.max_restart_count | type == "number" and . > 0) |
    [.pod_name, .max_restart_count] |
    join(" ")' <<< "${query_response}")

if [ -z "${failing_pods}" ]; then
  echo "No pod instances have been restarted"
  exit 0
fi

echo "Detected pod instances that have been restarted:\n$failing_pods"
exit 1