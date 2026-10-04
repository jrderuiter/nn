package config

import (
	"fmt"
	"os"
	"strings"
)

// EnvPrefix is the prefix of every environment variable nn reads.
const EnvPrefix = "NN_"

// envName is the variable that carries a configuration key.
//
// The mapping is the usual one: the prefix, then the key path in upper case
// with dots turned into underscores. So nono.network_profile is
// NN_NONO_NETWORK_PROFILE and tools.kubernetes.context is
// NN_TOOLS_KUBERNETES_CONTEXT.
func envName(path string) string {
	return EnvPrefix + strings.ToUpper(strings.ReplaceAll(path, ".", "_"))
}

// applyEnv overlays the environment on the merged configuration.
//
// It works from the known keys outward rather than by parsing variable names.
// Parsing cannot work: NN_NONO_NETWORK_PROFILE would be ambiguous between
// nono.network_profile and nono.network.profile, and only the schema settles
// it.
func applyEnv(dst map[string]any, keys []Key) error {
	for _, k := range keys {
		if k.Enable {
			// The naming rule makes NN_TOOLS_<NAME>_ENABLED the obvious guess
			// for enabled = true, but nothing reads it. Silence would leave the
			// tool in the state the user tried to change.
			name := envName(k.Path + "." + EnabledKey)
			if _, set := os.LookupEnv(name); set {
				return fmt.Errorf("%s is not supported, set %s to true or false", name, envName(k.Path))
			}
		}
		raw, ok := os.LookupEnv(envName(k.Path))
		if !ok {
			continue
		}
		path := strings.Split(k.Path, ".")
		if !k.Enable {
			setPath(dst, path, k.parse(raw))
			continue
		}
		if truthy(raw) {
			// Only create the section, or turn on one that a file switched
			// off. A section that a file already wrote keeps its settings.
			section, _ := lookup(dst, path)
			table, ok := section.(map[string]any)
			if !ok {
				table = map[string]any{}
				setPath(dst, path, table)
			}
			delete(table, EnabledKey)
			continue
		}
		remove(dst, path)
	}
	return nil
}

func truthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

func lookup(dst map[string]any, keys []string) (any, bool) {
	cur := dst
	for _, k := range keys[:len(keys)-1] {
		next, ok := cur[k].(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	v, ok := cur[keys[len(keys)-1]]
	return v, ok
}

func remove(dst map[string]any, keys []string) {
	cur := dst
	for _, k := range keys[:len(keys)-1] {
		next, ok := cur[k].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
	delete(cur, keys[len(keys)-1])
}

// Key is one configuration value that the environment can set.
type Key struct {
	Path string
	// List says the value is a list, which the environment gives as a comma
	// separated string.
	List bool
	// Enable says the path names a whole tool rather than a setting. A true
	// value writes the section and overrides enabled = false in a file, a
	// false value removes it. A runtime has no settings, so this is the only
	// way the environment can turn one on.
	Enable bool
}

func (k Key) parse(raw string) any {
	if !k.List {
		return raw
	}
	var out []any
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// setPath writes value at a dotted path, creating tables on the way.
func setPath(dst map[string]any, keys []string, value any) {
	cur := dst
	for _, k := range keys[:len(keys)-1] {
		next, ok := cur[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[k] = next
		}
		cur = next
	}
	cur[keys[len(keys)-1]] = value
}
