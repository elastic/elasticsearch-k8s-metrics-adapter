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

package config

import "regexp"

type Namer interface {
	// Register registers a new alias for a given metric. It returns the new metric name as it is exposed by the provider.
	Register(source string) string
	// Get returns the original name of the metric
	Get(alias string) (string, bool)
}

type identity struct{}

func (i *identity) Register(source string) string { return source }

func (i *identity) Get(alias string) (string, bool) {
	return alias, true
}

var _ Namer = &identity{}

type namer struct {
	aliases map[string]string
	matches *regexp.Regexp
	as      string
}

var _ Namer = &namer{}

func NewNamer(matches *Matches) (Namer, error) {
	if matches == nil {
		return &identity{}, nil
	}
	compiledMatches, err := regexp.Compile(matches.Matches)
	if err != nil {
		return nil, err
	}
	return &namer{
		aliases: make(map[string]string),
		matches: compiledMatches,
		as:      matches.As,
	}, nil
}

func (a *namer) Register(source string) string {
	if !a.matches.MatchString(source) {
		a.aliases[source] = source
		return source
	}
	matches := a.matches.FindStringSubmatchIndex(source)
	out := a.matches.ExpandString(nil, a.as, source, matches)
	alias := string(out)
	a.aliases[alias] = source
	return alias
}

func (a *namer) Get(alias string) (string, bool) {
	alias, ok := a.aliases[alias]
	return alias, ok
}
