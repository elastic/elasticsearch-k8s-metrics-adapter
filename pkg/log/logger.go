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

package log

import (
	"flag"
	"os"
	"strconv"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/spf13/pflag"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"k8s.io/component-base/logs"
	"k8s.io/klog/v2"

	"go.elastic.co/apm/module/apmzap/v2"
	"go.elastic.co/ecszap"
)

const defaultVerbosity = 0

var (
	logger logr.Logger
)

// Configure configures the main logger of this go program using the ECS support for uber-go/zap logger.
// go-logr logging levels convention is followed (reversed compared to zap logging levels): -1=error, 0=info, 1=debug.
// All k8s client logging is only enabled at the debug log level, it is disabled by default at lower logging levels.
func Configure(flagSets *pflag.FlagSet, serviceType string, serviceVersion string) func() {
	verbosity := defaultVerbosity
	verbosityFlag := flagSets.Lookup("v")
	if verbosityFlag != nil {
		verbosity, _ = strconv.Atoi(verbosityFlag.Value.String())
	}

	zapLevel := zapcore.Level(verbosity * -1)

	// using ecszap module to generate new zap.Core for zap.Logger
	encoderConfig := ecszap.NewDefaultEncoderConfig()
	core := ecszap.NewCore(encoderConfig, os.Stderr, zapLevel)

	// using zap module to generate zap.logger
	zapLogger := zap.
		New(
			core,                                    // ECS core
			zap.AddCaller(),                         // populate caller
			zap.AddStacktrace(zapcore.ErrorLevel),   // populate stack trace
			zap.WrapCore((&apmzap.Core{}).WrapCore), // sends errors to APM
		).
		With(
			zap.String("service.type", serviceType),
			zap.String("service.version", serviceVersion),
		)

	// using zapr module to generate logr.Logger
	logger = zapr.NewLogger(zapLogger)

	// enable k8s client logging only at debug log level
	if verbosity > 0 {
		klog.SetLogger(logger.WithName("k8s"))
	} else {
		// muted at info/error log level
		klog.SetLogger(zapr.NewLogger(zap.New(nil)))
	}

	// Propagate the set log level to klog
	flagset := flag.NewFlagSet("", flag.ContinueOnError)
	klog.InitFlags(flagset)
	_ = flagset.Set("v", strconv.Itoa(verbosity))

	logs.InitLogs()
	return func() {
		logs.FlushLogs()
	}
}

func ForPackage(name string) logr.Logger {
	return logger.WithName(name)
}
