package config

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// nonoTokens are variables nono expands itself, inside profile.json. Someone
// will reach for them here because that is where they work; a generic
// "unknown variable" would be true but unhelpful.
var nonoTokens = map[string]bool{
	"WORKDIR":       true,
	"NONO_CONFIG":   true,
	"NONO_PACKAGES": true,
}

// UnknownVarError reports variables that could not be expanded.
type UnknownVarError struct {
	Key  string
	Vars []string
	Nono bool // at least one was a nono profile token
}

func (e *UnknownVarError) Error() string {
	vars := "$" + strings.Join(e.Vars, ", $")
	if e.Nono {
		return fmt.Sprintf("env.%s references %s\n"+
			"    those are expanded by nono inside profile.json, not by nn.\n"+
			"    values in nn.yml expand against your shell environment only —\n"+
			"    use ${PWD} or an absolute path here", e.Key, vars)
	}
	return fmt.Sprintf("env.%s references undefined %s\n"+
		"    nn does not substitute an empty string for an unset variable;\n"+
		"    export it, or write the value literally (use $$ for a literal $)",
		e.Key, vars)
}

// ExpandValue substitutes $VAR and ${VAR} in raw against host.
//
// Expansion reads the host environment only, never other env: entries. That
// keeps every value independent of every other, which is what makes it safe
// for env: to be an unordered map — and it makes
// PATH: "${HOME}/.local/bin:${PATH}" mean the inherited PATH, not a
// half-assembled one.
func ExpandValue(key, raw string, host map[string]string) (string, error) {
	var unknown []string
	var sawNono bool

	out := os.Expand(raw, func(name string) string {
		if name == "$" { // os.Expand turns $$ into a lookup of "$"
			return "$"
		}
		if v, ok := host[name]; ok {
			return v
		}
		if nonoTokens[name] {
			sawNono = true
		}
		unknown = append(unknown, name)
		return ""
	})

	if len(unknown) > 0 {
		sort.Strings(unknown)
		return "", &UnknownVarError{Key: key, Vars: dedupe(unknown), Nono: sawNono}
	}
	return out, nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// EnvironMap turns a K=V slice into a map.
func EnvironMap(environ []string) map[string]string {
	m := make(map[string]string, len(environ))
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}
