// Package runtime registers one tool per language runtime or version manager.
//
// Each one wraps a single nono group and adds what that group leaves out: the
// writable state and cache directories, and the environment variables the
// minimal base list would otherwise filter away. Each is named after the
// program rather than the group, because the program is what you declare and
// the group is how it is granted.
package runtime

import (
	"context"
	goruntime "runtime"
	"slices"
	"sort"

	"github.com/BurntSushi/toml"

	"github.com/jrderuiter/nn/internal/nono"
	"github.com/jrderuiter/nn/internal/tool"
)

// Config is the table of one runtime tool. The tools need no settings yet,
// so declaring the tool at all is what turns it on.
type Config struct{}

// MiseConfig is the [tools.mise] table, the one runtime with a setting.
type MiseConfig struct {
	// TrustWorkdir makes mise trust the configuration files in the working
	// directory, inside the sandbox only.
	TrustWorkdir bool `toml:"trust_workdir"`
}

// spec describes what a tool needs on top of its nono group.
type spec struct {
	// group is the nono policy group that grants the read-only parts.
	group string
	// read and allow are the paths the group misses.
	read  []string
	allow []nono.CondPath
	// allowVars are the environment variables the tool needs.
	allowVars []string
}

// specs maps a tool name to what it grants. Each group was checked with
// `nono profile groups <name>`, and only the gaps are listed here.
var specs = map[string]spec{
	// mise_manager grants read on /etc/mise, ~/.local/bin/mise, ~/.config/mise
	// and ~/.local/share/mise, and nothing writable. On macOS mise keeps its
	// cache under ~/Library/Caches, not ~/.cache.
	"mise": {
		group: "mise_manager",
		allow: []nono.CondPath{
			nono.P("$HOME/.local/state/mise"),
			nono.P("$HOME/.cache/mise"),
			nono.PWhen("$HOME/Library/Caches/mise", "macos"),
		},
	},
	// go_runtime grants read on ~/go and /usr/local/go. The module cache and
	// the build cache sit under those and must be writable, or every build
	// fails on the first download.
	"go": {
		group:     "go_runtime",
		allow:     []nono.CondPath{nono.P("$HOME/go/pkg/mod"), nono.P("$XDG_CACHE_HOME/go-build")},
		allowVars: []string{"GO*", "CGO_*"},
	},
	"node": {
		group:     "node_runtime",
		allowVars: []string{"NODE_*", "NPM_*"},
	},
	"bun": {
		group: "bun_runtime",
	},
	"python": {
		group:     "python_runtime",
		allowVars: []string{"PYTHON*", "PIP_*"},
	},
	"rust": {
		group:     "rust_runtime",
		allowVars: []string{"CARGO_*", "RUST*"},
	},
	"java": {
		group:     "java_runtime",
		allowVars: []string{"JAVA_*"},
	},
	"nix": {
		group: "nix_runtime",
	},
}

func init() {
	for name := range specs {
		proto := func() any { return &Config{} }
		if name == "mise" {
			proto = func() any { return &MiseConfig{} }
		}
		tool.Register(name, factoryFor(name), proto)
	}
}

func factoryFor(name string) tool.Factory {
	return func(md toml.MetaData, prim toml.Primitive) (tool.Provider, error) {
		p := &provider{name: name, spec: specs[name]}
		if name != "mise" {
			var cfg Config
			return p, md.PrimitiveDecode(prim, &cfg)
		}
		var cfg MiseConfig
		if err := md.PrimitiveDecode(prim, &cfg); err != nil {
			return nil, err
		}
		// mise trusts a configuration file by its path, so a fresh worktree
		// is untrusted even when its repository is trusted. The value is
		// expanded by nono, so the profile stays portable. The trust reaches
		// only the sandbox: the host mise state does not change.
		if cfg.TrustWorkdir {
			p.setVars = map[string]string{"MISE_TRUSTED_CONFIG_PATHS": "$WORKDIR"}
		}
		return p, nil
	}
}

// Names lists the runtimes, sorted, for help text and for the run order.
func Names() []string {
	out := make([]string, 0, len(specs))
	for name := range specs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

type provider struct {
	name    string
	spec    spec
	setVars map[string]string
}

func (p *provider) Name() string { return p.name }

func (p *provider) Preflight(ctx context.Context, e *tool.Env) error { return nil }

func (p *provider) Build(ctx context.Context, e *tool.Env) (*tool.Result, error) {
	f := &nono.Profile{
		Groups: &nono.Groups{Include: []nono.CondName{nono.G(p.spec.group)}},
	}
	if len(p.spec.read) > 0 || len(p.spec.allow) > 0 {
		f.Filesystem = &nono.Filesystem{}
		for _, r := range p.spec.read {
			f.Filesystem.Read = append(f.Filesystem.Read, nono.P(r))
		}
		f.Filesystem.Allow = append(f.Filesystem.Allow, p.spec.allow...)
	}
	if len(p.spec.allowVars) > 0 || len(p.setVars) > 0 {
		f.Environment = &nono.Environment{AllowVars: p.spec.allowVars, SetVars: p.setVars}
	}
	return &tool.Result{Fragment: f, EnsureDirs: ensureDirs(p.spec.allow, goruntime.GOOS)}, nil
}

// ensureDirs lists the paths to create on this platform. The profile keeps
// every entry, so it stays the same bytes on every machine, but a macOS path
// must not appear as an empty directory on Linux.
func ensureDirs(paths []nono.CondPath, goos string) []string {
	platform := map[string]string{"darwin": "macos"}[goos]
	if platform == "" {
		platform = goos
	}
	var out []string
	for _, c := range paths {
		if len(c.When) == 0 || slices.Contains(c.When, platform) {
			out = append(out, c.Path)
		}
	}
	return out
}
