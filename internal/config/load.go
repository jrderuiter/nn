package config

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// profileExpressible maps an nn-looking key to where it actually belongs in
// profile.json. These are flags nono can already express in a profile, so nn
// deliberately has no typed key for them — and the resulting "unknown field"
// error would otherwise be accurate but useless.
var profileExpressible = map[string]string{
	"allow":              "filesystem.allow",
	"read":               "filesystem.read",
	"write":              "filesystem.write",
	"allow_file":         "filesystem.allow",
	"read_file":          "filesystem.read",
	"write_file":         "filesystem.write",
	"block_net":          "network.block",
	"network_profile":    "network.network_profile",
	"allow_domain":       "network.allow_domain",
	"deny_domain":        "network.deny_domain",
	"credential":         "network.credentials",
	"allow_endpoint":     "network.custom_credentials.<name>.endpoint_rules",
	"env_credential":     "env_credentials",
	"env_credential_map": "env_credentials",
	"open_port":          "network.open_port",
	"listen_port":        "network.listen_port",
	"allow_connect_port": "network.connect_port",
	"allow_http2":        "network.allow_http2",
	"allow_command":      "commands.allow",
	"block_command":      "commands.deny",
	"upstream_proxy":     "network.upstream_proxy",
	"upstream_bypass":    "network.upstream_bypass",
	"set_vars":           "environment.set_vars",
	"allow_vars":         "environment.allow_vars",
	"deny_vars":          "environment.deny_vars",
	"groups":             "groups",
	"packs":              "packs",
	"skipdirs":           "skipdirs (or nn's `skip_dir`)",
}

var unknownFieldRE = regexp.MustCompile(`unknown field "([^"]+)"`)

// Load reads and validates a config file.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	// Strict decoding is how the "gap flags only" rule is enforced: anything
	// the profile can express has no field here, so it lands as unknown.
	if err := yaml.UnmarshalWithOptions(data, &f, yaml.DisallowUnknownField()); err != nil {
		return nil, enrichDecodeError(path, err)
	}
	if err := Validate(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &f, nil
}

// enrichDecodeError keeps goccy's annotated output — it already carries the
// line, column and a caret — and appends the reason the key is unknown.
func enrichDecodeError(path string, err error) error {
	m := unknownFieldRE.FindStringSubmatch(err.Error())
	if m == nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	key := m[1]
	body := strings.TrimRight(err.Error(), "\n")

	var hint string
	switch {
	case key == "mode":
		hint = "`mode` was removed — use the `nn run` / `nn shell` / `nn wrap` subcommands."
	case profileExpressible[key] != "":
		flag := "--" + strings.ReplaceAll(key, "_", "-")
		hint = fmt.Sprintf(
			"`%s` is expressible in the nono profile — set %s in your profile.json instead.\n"+
				"nn only carries flags that a profile cannot express.",
			flag, profileExpressible[key])
	default:
		if near := suggest(key); near != "" {
			hint = fmt.Sprintf("did you mean %q?", near)
		} else {
			hint = "see `nn init` for the full set of supported keys"
		}
	}
	return fmt.Errorf("%s:\n%s\n\n%s", path, body, hint)
}

// knownKeys is every key a config may contain, for "did you mean".
func knownKeys() []string {
	seen := map[string]bool{}
	var out []string
	add := func(k string) {
		if k != "" && k != "-" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	rt := reflect.TypeOf(Section{})
	for i := range rt.NumField() {
		add(yamlKey(rt.Field(i)))
	}
	for _, m := range []Mode{ModeRun, ModeShell, ModeWrap} {
		add(m.String())
	}
	sort.Strings(out)
	return out
}

// suggest returns the closest known key within edit distance 2, or "".
func suggest(key string) string {
	best, bestDist := "", 3
	for _, k := range knownKeys() {
		if d := levenshtein(key, k); d < bestDist {
			best, bestDist = k, d
		}
	}
	return best
}

func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// KnownKeys is exported for doctor and tests.
func KnownKeys() []string { return slices.Clone(knownKeys()) }
