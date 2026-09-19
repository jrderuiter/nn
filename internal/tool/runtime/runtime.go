// Package runtime registers one tool per language runtime or version manager.
//
// Each one wraps a single nono group and adds what that group leaves out: the
// writable state and cache directories, and the environment variables the
// minimal base list would otherwise filter away. The capability is named after
// the tool rather than the group, because the tool is what you declare and the
// group is how it is granted.
package runtime

import (
	"context"
	"sort"

	"github.com/BurntSushi/toml"

	"github.com/jrderuiter/nn/internal/nono"
	"github.com/jrderuiter/nn/internal/tool"
)

// Config is the table of one tool tool. The tools need no settings yet,
// so declaring the capability at all is what turns it on.
type Config struct{}

// spec describes what a tool needs on top of its nono group.
type spec struct {
	// group is the nono policy group that grants the read-only parts.
	group string
	// read and allow are the paths the group misses.
	read  []string
	allow []string
	// allowVars are the environment variables the tool needs.
	allowVars []string
}

// specs maps a tool name to what it grants. Each group was checked with
// `nono profile groups <name>`, and only the gaps are listed here.
var specs = map[string]spec{
	// mise_manager grants read on /etc/mise, ~/.local/bin/mise, ~/.config/mise
	// and ~/.local/share/mise, and nothing writable.
	"mise": {
		group: "mise_manager",
		allow: []string{"$HOME/.local/state/mise", "$HOME/.cache/mise"},
	},
	// go_runtime grants read on ~/go and /usr/local/go. The module cache and
	// the build cache sit under those and must be writable, or every build
	// fails on the first download.
	"go": {
		group:     "go_runtime",
		allow:     []string{"$HOME/go/pkg/mod", "$XDG_CACHE_HOME/go-build"},
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
		tool.Register(name, factoryFor(name), func() any { return &Config{} })
	}
}

func factoryFor(name string) tool.Factory {
	return func(md toml.MetaData, prim toml.Primitive) (tool.Provider, error) {
		var cfg Config
		if err := md.PrimitiveDecode(prim, &cfg); err != nil {
			return nil, err
		}
		return &provider{name: name, spec: specs[name]}, nil
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
	name string
	spec spec
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
		for _, a := range p.spec.allow {
			f.Filesystem.Allow = append(f.Filesystem.Allow, nono.P(a))
		}
	}
	if len(p.spec.allowVars) > 0 {
		f.Environment = &nono.Environment{AllowVars: p.spec.allowVars}
	}
	return &tool.Result{Fragment: f, EnsureDirs: p.spec.allow}, nil
}
