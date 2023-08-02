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