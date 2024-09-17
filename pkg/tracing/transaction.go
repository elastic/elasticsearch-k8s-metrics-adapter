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

package tracing

import (
	"context"
	"os"
	"runtime"
	"strings"

	"go.elastic.co/apm/v2"
)

func IsEnabled() bool {
	_, apmURL := os.LookupEnv("ELASTIC_APM_SERVER_URL")
	return apmURL
}

// NewTransaction starts a new transaction and sets up a new context with that transaction that also contains the related
// APM agent's tracer.
func NewTransaction(ctx context.Context, t *apm.Tracer, txName, txType string) (*apm.Transaction, context.Context) {
	if t == nil {
		return nil, ctx // apm turned off
	}
	tx := t.StartTransaction(txName, txType)
	return tx, apm.ContextWithTransaction(ctx, tx)
}

// EndTransaction nil safe version of APM agents tx.End()
func EndTransaction(tx *apm.Transaction) {
	if tx != nil {
		tx.End()
	}
}

func Span(ctx *context.Context) func() {
	if apm.TransactionFromContext(*ctx) == nil {
		// no transaction in the context implicates disabled tracing, exiting early to avoid unnecessary work
		return func() {}
	}

	pc, _, _, ok := runtime.Caller(1)

	name := "unknown_function"
	if ok {
		f := runtime.FuncForPC(pc)
		name = f.Name()
		// cut module and package name, leave only func name

		lastDot := strings.LastIndex(name, ".")
		// if something went wrong and dot is not present or last, let's not crash the operator and use full name instead
		if 0 <= lastDot && lastDot < len(name)-1 {
			name = name[lastDot+1:]
		}
	}

	span, newCtx := apm.StartSpan(*ctx, name, "app")
	*ctx = newCtx

	return func() {
		span.End()
	}
}
