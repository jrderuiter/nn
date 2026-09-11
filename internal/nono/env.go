package nono

import (
	"sort"
	"strings"

	"github.com/jderuiter/nn/internal/config"
)

// EnvOverrides are the CLI-supplied -e / -u values, which outrank the config.
type EnvOverrides struct {
	Set   map[string]string
	Unset []string
}

// BuildEnv produces the environment for the exec'd chain: the host
// environment, with config and CLI overrides applied and removals dropped.
//
// Precedence, low to high: host env, env:, -e. Removals (env_unset, -u) apply
// last and beat everything, so `nn -u NONO_THEME` clears a var the config set.
//
// Overrides replace their variable in place rather than being appended.
// execve is last-wins so either works, but in-place means `nn print` shows one
// PATH= rather than two, and the inherited order stays stable so two prints
// differ only where something actually changed.
func BuildEnv(s *config.Section, cli EnvOverrides, environ []string) (env []string, applied map[string]string, removed []string, err error) {
	host := config.EnvironMap(environ)

	applied = make(map[string]string, len(s.Env)+len(cli.Set))
	for _, k := range sortedKeys(s.Env) { // sorted so errors are deterministic
		v, err := config.ExpandValue(k, s.Env[k], host)
		if err != nil {
			return nil, nil, nil, err
		}
		applied[k] = v
	}
	// CLI values are used verbatim: they came through a shell that already
	// expanded them, and expanding again would be a double-expansion bug.
	for k, v := range cli.Set {
		applied[k] = v
	}

	unset := map[string]bool{}
	for _, k := range append(append([]string{}, s.EnvUnset...), cli.Unset...) {
		unset[k] = true
		delete(applied, k)
	}

	seen := map[string]bool{}
	env = make([]string, 0, len(environ)+len(applied))
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case unset[k]:
			removed = append(removed, k)
		case hasKey(applied, k):
			env = append(env, k+"="+applied[k])
			seen[k] = true
		default:
			env = append(env, kv)
		}
	}
	for _, k := range sortedKeys(applied) {
		if !seen[k] {
			env = append(env, k+"="+applied[k])
		}
	}
	sort.Strings(removed)
	return env, applied, removed, nil
}

func hasKey(m map[string]string, k string) bool { _, ok := m[k]; return ok }

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
