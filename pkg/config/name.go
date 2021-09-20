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
