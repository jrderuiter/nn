// Package github grants GitHub API and git-over-HTTPS access through nono's
// credential proxy, with the token supplied by fnox on demand.
package github

import (
	"context"
	"fmt"
	"strconv"

	"github.com/BurntSushi/toml"

	"github.com/jrderuiter/nn/internal/nono"
	"github.com/jrderuiter/nn/internal/tool"
	"github.com/jrderuiter/nn/internal/workspace"
)

// Config is the [tools.github] table.
type Config struct {
	// Secret is the fnox key that holds the token.
	Secret string `toml:"secret" help:"fnox key holding the token"`
	// Git allows clone, fetch and push over HTTPS. It is on by default,
	// because an agent with API access but no git access cannot do the thing
	// it was given the repository for.
	Git *bool `toml:"git" help:"allow clone, fetch and push over HTTPS"`
	// RewriteSSH turns an ssh remote into its HTTPS equivalent inside the
	// sandbox. Without it a repository cloned over ssh keeps using ssh, which
	// carries no credential the proxy can inject, so fetch fails.
	RewriteSSH *bool `toml:"rewrite_ssh" help:"use HTTPS for github.com remotes written as ssh"`
	// GHCLI redirects the gh configuration directory into the project, so the
	// agent never touches the host gh state.
	GHCLI *bool `toml:"gh_cli" help:"redirect the gh configuration into the project"`
	// CacheTTLSecs is how long nono caches the captured token.
	CacheTTLSecs int `toml:"cache_ttl_secs" help:"how long nono caches the captured token"`
}

type provider struct{ cfg Config }

func init() {
	// The prototype describes the shape, not the defaults: it is only read for
	// the field names that the environment can set.
	tool.Register("github", New, func() any { return &Config{} })
}

// New decodes the tool's own table.
func New(md toml.MetaData, prim toml.Primitive) (tool.Provider, error) {
	// The optional booleans start nil and default to true afterwards. They
	// cannot be pre-filled: the decoder writes through an existing pointer, so
	// sharing one variable would let a single false value turn all of them off.
	cfg := Config{Secret: "GITHUB_TOKEN", CacheTTLSecs: 900}
	if err := md.PrimitiveDecode(prim, &cfg); err != nil {
		return nil, err
	}
	if cfg.Secret == "" {
		return nil, fmt.Errorf("secret must not be empty")
	}
	for _, f := range []**bool{&cfg.GHCLI, &cfg.Git, &cfg.RewriteSSH} {
		if *f == nil {
			v := true
			*f = &v
		}
	}
	return &provider{cfg}, nil
}

func (p *provider) Name() string { return "github" }

func (p *provider) Preflight(ctx context.Context, e *tool.Env) error {
	return e.Secrets.Check(ctx, p.cfg.Secret)
}

func (p *provider) Build(ctx context.Context, e *tool.Env) (*tool.Result, error) {
	capture := e.Secrets.CaptureCommand(p.cfg.Secret)

	f := &nono.Profile{
		CredentialCapture: map[string]nono.CredentialCapture{
			"github": {Command: capture, TimeoutSecs: 10, CacheTTLSecs: p.cfg.CacheTTLSecs},
		},
		Network: &nono.Network{
			Credentials: []string{"github"},
			CustomCredentials: map[string]nono.CustomCredential{
				"github": {
					Upstream:      "https://api.github.com",
					CredentialKey: "cmd://github",
					EnvVar:        "GITHUB_TOKEN",
					InjectMode:    "header",
					InjectHeader:  "Authorization",
					CredentialFmt: "token {}",
				},
			},
		},
		// Only what the sandbox needs from the host. The token arrives by
		// injection and GH_CONFIG_DIR and the GIT_CONFIG_* entries are set
		// below, so none of them needs passing through.
		Environment: &nono.Environment{},
	}

	if *p.cfg.Git {
		// A second route is needed because git speaks to github.com, not to
		// the API host, and it authenticates with basic auth rather than a
		// bearer header. It shares the one capture: the token is the same, and
		// a second capture would mean a second fnox call and a second cache.
		f.Network.Credentials = append(f.Network.Credentials, "github_git")
		f.Network.CustomCredentials["github_git"] = nono.CustomCredential{
			Upstream:      "https://github.com",
			CredentialKey: "cmd://github",
			EnvVar:        "GITHUB_GIT_AUTH",
			InjectMode:    "basic_auth",
		}
		if *p.cfg.RewriteSSH {
			f.Environment.SetVars = sshRewrite()
		}
	}

	if *p.cfg.GHCLI {
		// No filesystem grant: this sits inside the artifact directory, which
		// the base profile already grants recursively.
		if f.Environment.SetVars == nil {
			f.Environment.SetVars = map[string]string{}
		}
		f.Environment.SetVars["GH_CONFIG_DIR"] = workspace.ProfileVar + "/gh"
	}

	return &tool.Result{Fragment: f}, nil
}

// sshRewrite makes git use HTTPS for github.com even when a remote is written
// as ssh.
//
// The proxy can only add a credential to an HTTPS request, so an ssh remote
// would leave the sandbox with no way to authenticate, and the agent sees a
// permission error on the first fetch. Both spellings of an ssh remote are
// covered.
//
// This uses the GIT_CONFIG_COUNT form rather than a config file, so nothing is
// written and the host git configuration is untouched. It needs git 2.31 or
// newer.
func sshRewrite() map[string]string {
	rewrites := []struct{ from, to string }{
		{"git@github.com:", "https://github.com/"},
		{"ssh://git@github.com/", "https://github.com/"},
	}
	out := map[string]string{"GIT_CONFIG_COUNT": strconv.Itoa(len(rewrites))}
	for i, r := range rewrites {
		out["GIT_CONFIG_KEY_"+strconv.Itoa(i)] = "url." + r.to + ".insteadOf"
		out["GIT_CONFIG_VALUE_"+strconv.Itoa(i)] = r.from
	}
	return out
}
