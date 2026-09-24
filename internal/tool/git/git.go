// Package git grants the parts of a git setup that a sandboxed agent needs: a
// committer identity, the host git configuration, and the hosts it may reach.
//
// It does not pass the host ssh agent socket through. Pushing over ssh would
// need that socket, and the github tool rewrites github.com remotes to HTTPS
// instead, where the proxy injects a token that the sandbox never sees.
package git

import (
	"context"
	"fmt"

	"github.com/BurntSushi/toml"

	"github.com/jrderuiter/nn/internal/nono"
	"github.com/jrderuiter/nn/internal/tool"
)

// Config is the [tools.git] table.
type Config struct {
	// Name and Email set the committer identity inside the sandbox.
	Name  string `toml:"name"`
	Email string `toml:"email"`
	// Hosts are extra git hosts to allow, for example "gitlab.com".
	Hosts []string `toml:"hosts"`
	// Config grants read access to the host git configuration.
	Config *bool `toml:"config"`
}

type provider struct{ cfg Config }

func init() {
	// The prototype describes the shape, not the defaults: it is only read for
	// the field names the environment can set.
	tool.Register("git", New, func() any { return &Config{} })
}

// New decodes the tool's own table.
func New(md toml.MetaData, prim toml.Primitive) (tool.Provider, error) {
	// Left nil and defaulted afterwards: the decoder writes through an
	// existing pointer, so a pre-filled default would be overwritten in place.
	var cfg Config
	if err := md.PrimitiveDecode(prim, &cfg); err != nil {
		return nil, err
	}
	if cfg.Config == nil {
		v := true
		cfg.Config = &v
	}
	if (cfg.Name == "") != (cfg.Email == "") {
		return nil, fmt.Errorf("name and email must be set together")
	}
	return &provider{cfg}, nil
}

func (p *provider) Name() string { return "git" }

func (p *provider) Preflight(ctx context.Context, e *tool.Env) error { return nil }

func (p *provider) Build(ctx context.Context, e *tool.Env) (*tool.Result, error) {
	// No allow_vars. The identity below goes through set_vars, which nono
	// applies after the filter, so passing host GIT_* variables would only
	// widen what the sandbox inherits.
	f := &nono.Profile{Environment: &nono.Environment{}}

	if *p.cfg.Config {
		f.Groups = &nono.Groups{Include: []nono.CondName{nono.G("git_config")}}
	}

	if p.cfg.Name != "" {
		f.Environment.SetVars = map[string]string{
			"GIT_AUTHOR_NAME":     p.cfg.Name,
			"GIT_AUTHOR_EMAIL":    p.cfg.Email,
			"GIT_COMMITTER_NAME":  p.cfg.Name,
			"GIT_COMMITTER_EMAIL": p.cfg.Email,
		}
	}

	for _, h := range p.cfg.Hosts {
		if f.Network == nil {
			f.Network = &nono.Network{}
		}
		f.Network.AllowDomain = append(f.Network.AllowDomain, nono.Domain{Domain: h})
	}

	return &tool.Result{Fragment: f}, nil
}
