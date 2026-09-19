// Package git grants the parts of a git setup that a sandboxed agent needs:
// the host SSH agent, a committer identity, and the hosts it may reach.
package git

import (
	"context"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"

	"github.com/jrderuiter/nn/internal/nono"
	"github.com/jrderuiter/nn/internal/tool"
)

// Config is the [tools.git] table.
type Config struct {
	// SSH grants the host SSH agent socket, so the agent can push over SSH
	// without ever seeing a private key.
	SSH bool `toml:"ssh" help:"pass the host ssh agent socket through"`
	// Name and Email set the committer identity inside the sandbox.
	Name  string `toml:"name" help:"committer name inside the sandbox"`
	Email string `toml:"email" help:"committer email inside the sandbox"`
	// Hosts are extra git hosts to allow, for example "gitlab.com".
	Hosts []string `toml:"hosts" help:"extra git hosts to allow"`
	// Config grants read access to the host git configuration.
	Config *bool `toml:"config" help:"grant read access to the host git configuration"`
}

type provider struct{ cfg Config }

func init() { tool.Register("git", New, func() any { on := true; return &Config{Config: &on} }) }

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

func (p *provider) Preflight(ctx context.Context, e *tool.Env) error {
	if !p.cfg.SSH {
		return nil
	}
	sock, ok := e.Lookup("SSH_AUTH_SOCK")
	if !ok || sock == "" {
		return fmt.Errorf("ssh = true but SSH_AUTH_SOCK is not set on the host; start an ssh agent or set ssh = false")
	}
	if _, err := os.Stat(sock); err != nil {
		return fmt.Errorf("SSH_AUTH_SOCK points at %s, which is not reachable: %w", sock, err)
	}
	return nil
}

func (p *provider) Build(ctx context.Context, e *tool.Env) (*tool.Result, error) {
	f := &nono.Profile{
		Environment: &nono.Environment{AllowVars: []string{"GIT_*"}},
	}

	if *p.cfg.Config {
		f.Groups = &nono.Groups{Include: []nono.CondName{nono.G("git_config")}}
	}

	if p.cfg.SSH {
		sock, _ := e.Lookup("SSH_AUTH_SOCK")
		// unix_socket grants connect on the socket itself and implies read on
		// that file. A plain filesystem grant is not enough once the network
		// filter is active.
		f.Filesystem = &nono.Filesystem{UnixSocket: []nono.CondPath{nono.P(sock)}}
		// The variable only goes through when the socket behind it does. With
		// ssh off it would name a path the sandbox cannot reach, which turns a
		// clear configuration choice into a confusing connection error.
		f.Environment.AllowVars = append(f.Environment.AllowVars, "SSH_AUTH_SOCK")
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
