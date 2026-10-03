package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// FileName is the project configuration file.
const FileName = "nn.toml"

// Load reads the user level configuration, then the nearest project file, then
// applies the command line overrides.
//
// The layers are merged as plain maps before anything is decoded into the
// configuration struct. Decoding each file in turn would not work: a tool
// table is decoded lazily by its provider, and a second decode would replace
// the first table rather than merge into it.
// Options selects the layers that Load reads.
type Options struct {
	Dir      string
	Explicit string
	// Keys are the configuration values the environment may set. The caller
	// supplies them because the tool keys come from the tool registry.
	Keys []Key
}

func Load(o Options) (*Config, error) {
	files, err := configFiles(o.Dir, o.Explicit)
	if err != nil {
		return nil, err
	}

	merged := map[string]any{}
	for _, f := range files {
		var m map[string]any
		if _, err := toml.DecodeFile(f, &m); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		mergeMaps(merged, m)
	}

	applyEnv(merged, o.Keys)

	if err := pruneDisabled(merged); err != nil {
		return nil, fmt.Errorf("%s: %w", describe(files), err)
	}

	if len(merged) == 0 {
		// No configuration at all is a valid state: `nn run -- claude` with
		// defaults only.
		return &Config{sources: files}, nil
	}

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(merged); err != nil {
		return nil, fmt.Errorf("cannot re-encode the merged configuration: %w", err)
	}
	cfg := &Config{}
	md, err := toml.Decode(buf.String(), cfg)
	if err != nil {
		return nil, fmt.Errorf("merged configuration: %w", err)
	}
	if err := rejectUnknown(describe(files), md); err != nil {
		return nil, err
	}
	if err := checkAgentNames(describe(files), cfg.Agents); err != nil {
		return nil, err
	}
	cfg.md = md
	cfg.sources = files
	return cfg, nil
}

// agentNamePattern keeps an agent name safe to use in a file name, because
// each agent gets its own profile file.
var agentNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func checkAgentNames(source string, agents map[string]Agent) error {
	for name := range agents {
		if !agentNamePattern.MatchString(name) {
			return fmt.Errorf("%s: [agents.%s]: an agent name must be lower case letters, digits, - and _", source, name)
		}
	}
	return nil
}

func configFiles(dir, explicit string) ([]string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return nil, fmt.Errorf("configuration file %s: %w", explicit, err)
		}
		return []string{explicit}, nil
	}
	var out []string
	if u := userConfigPath(); u != "" {
		if _, err := os.Stat(u); err == nil {
			out = append(out, u)
		}
	}
	if p := Find(dir); p != "" {
		out = append(out, p)
	}
	return out, nil
}

func describe(files []string) string {
	if len(files) == 0 {
		return "the environment"
	}
	return strings.Join(files, ", ")
}

// EnabledKey switches a table under [tools] off. A merge only adds and
// replaces keys, and TOML has no null, so no layer can delete a table that a
// layer below it wrote. A switch merges like any other key, which lets a later
// file drop a tool, or one named entry of a tool, that an earlier file
// declares.
const EnabledKey = "enabled"

// pruneDisabled removes every table under [tools] whose enabled key is false.
// It runs after every layer has merged, so the last layer decides. It also
// drops the key from the tables that stay, so a provider never sees it.
func pruneDisabled(merged map[string]any) error {
	tools, ok := merged["tools"].(map[string]any)
	if !ok {
		return nil
	}
	return prune("tools", tools)
}

func prune(path string, table map[string]any) error {
	for k, v := range table {
		sub, ok := v.(map[string]any)
		if !ok {
			continue
		}
		p := path + "." + k
		if raw, set := sub[EnabledKey]; set {
			on, ok := raw.(bool)
			if !ok {
				return fmt.Errorf("%s.%s must be true or false, got %v", p, EnabledKey, raw)
			}
			delete(sub, EnabledKey)
			if !on {
				delete(table, k)
				continue
			}
		}
		if err := prune(p, sub); err != nil {
			return err
		}
	}
	return nil
}

// mergeMaps folds src into dst, descending into nested tables so a later layer
// adds to a tool rather than replacing it.
func mergeMaps(dst, src map[string]any) {
	for k, v := range src {
		sub, isMap := v.(map[string]any)
		if !isMap {
			dst[k] = v
			continue
		}
		existing, ok := dst[k].(map[string]any)
		if !ok {
			existing = map[string]any{}
			dst[k] = existing
		}
		mergeMaps(existing, sub)
	}
}

// Find walks up from dir looking for nn.toml and returns its path, or an empty
// string when there is none. It stops at a directory holding a .git entry, so a
// nested repository never picks up its parent's settings.
func Find(dir string) string {
	cur, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		p := filepath.Join(cur, FileName)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return ""
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

func userConfigPath() string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "nn", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "nn", "config.toml")
}

// rejectUnknown fails on a key that nn does not understand. Keys under
// [tools] are exempt, because each provider decodes its own sub-table
// and validates it there. So are the raw profile blocks, which are checked
// against the profile types when they are decoded.
func rejectUnknown(source string, md toml.MetaData) error {
	var bad []string
	for _, k := range md.Undecoded() {
		s := k.String()
		if strings.HasPrefix(s, "tools.") || strings.HasPrefix(s, "nono.profile.") {
			continue
		}
		if len(k) > 3 && k[0] == "agents" && k[2] == "profile" {
			continue
		}
		bad = append(bad, s)
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return fmt.Errorf("%s: unknown key(s): %s", source, strings.Join(bad, ", "))
}
