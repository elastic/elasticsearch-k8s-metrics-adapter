#!/usr/bin/env bash

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

# Check that the Elastic license is applied to all files.

set -eu

echo "Checking license headers"

# shellcheck disable=2086
: "${CHECK_PATH:=$(dirname $0)/../../*}" # root project directory

files=$(find . -path ./vendor -prune -o -type f \( -iname \*.go -o -iname \*.sh -o -iname Makefile \))

rc=0
for file in ${files}; do
  if [ -f "${file}" ]; then
    # Expected md5sum for license header is 5dd3abfa483f04ffc7b91241604b8cfe
    md5=$(head -n 16 "${file}" | md5sum | cut -d ' ' -f 1)
    if [[ "${md5}" != "5dd3abfa483f04ffc7b91241604b8cfe" && # *.go
          "${md5}" != "d072f309292b73b02a4a2bf0eb33a5e7" && # Makefiles
          "${md5}" != "45bb875410d1a7864db96c6367c3ad48" && # Shell scripts
          "${md5}" !=  "60d9ab7ece64d01a602eedf330400ec0" ]]; then # zz_generated.openapi.go
        echo "${file} does not have an expected license header: ${md5}"
        rc=1
    fi
  fi
done

exit ${rc}