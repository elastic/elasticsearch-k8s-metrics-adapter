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

TEMPLATE=$1
SEARCH_TARGET=$2
export QUERY_WINDOW=${QUERY_WINDOW:-10m}

if [ -z "${TARGET_ENV}" ]; then
  echo "Expected TARGET_ENV to be present in the environment" 1>&2
  exit 1
fi

case "${TARGET_ENV}" in
  qa )
    ELASTICSEARCH_CREDENTIALS_KEY="kv/ci-shared/serverless/quality-gates/qa-observability"
    ;;
  staging )
    ELASTICSEARCH_CREDENTIALS_KEY="kv/ci-shared/serverless/quality-gates/staging-observability"
    ;;
  * )
    echo "Unsupported target environment specified ${TARGET_ENV}" 1>&2
    exit 1
esac

ES_CREDENTIALS=$(vault kv get -format=json ${ELASTICSEARCH_CREDENTIALS_KEY})
ES_USERNAME=$(jq -r '.data.data.username' <<< "${ES_CREDENTIALS}")
ES_PASSWORD=$(jq -r '.data.data.password' <<< "${ES_CREDENTIALS}")
ES_HOST=$(jq -r '.data.data.host' <<< "${ES_CREDENTIALS}")

query=$(cat ${TEMPLATE} | envsubst)

curl -H "Content-Type: application/json" -u "${ES_USERNAME}:${ES_PASSWORD}" "${ES_HOST}/${SEARCH_TARGET}/_search" -d "${query}"