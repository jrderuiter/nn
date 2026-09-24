// Package github grants GitHub API and git-over-HTTPS access through nono's
// credential proxy, with the token supplied by fnox on demand.
package github

import (
	"context"
	"fmt"

	"github.com/BurntSushi/toml"

	"github.com/jrderuiter/nn/internal/nono"
	"github.com/jrderuiter/nn/internal/tool"
	"github.com/jrderuiter/nn/internal/workspace"
)

// Config is the [tools.github] table.
type Config struct {
	// Secret is the fnox key that holds the token.
	Secret string `toml:"secret"`
	// Git allows clone, fetch and push over HTTPS. It is on by default,
	// because an agent with API access but no git access cannot do the thing
	// it was given the repository for.
	Git *bool `toml:"git"`
	// RewriteSSH turns an ssh remote into its HTTPS equivalent inside the
	// sandbox. Without it a repository cloned over ssh keeps using ssh, which
	// carries no credential the proxy can inject, so fetch fails.
	RewriteSSH *bool `toml:"rewrite_ssh"`
	// GHCLI redirects the gh configuration directory into the project, so the
	// agent never touches the host gh state.
	GHCLI *bool `toml:"gh_cli"`
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
	cfg := Config{Secret: "GITHUB_TOKEN"}
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
	// env:// rather than cmd://. nn resolves the token once, while the user is
	// starting the agent, and hands it to nono in its environment. A cmd://
	// capture would run fnox mid-session instead.
	// One host variable per route. Sharing a credential_key across both would
	// bind them to a single broker credential, and a phantom issued for the
	// basic-auth route resolves to a user:token pair, which is not what the
	// API route's caller expects.
	const (
		tokenVar = "NN_GITHUB_TOKEN"
		gitVar   = "NN_GITHUB_GIT_AUTH"
	)

	f := &nono.Profile{
		Network: &nono.Network{
			// The routes below only inject a credential. The hosts still have
			// to be in the allowlist, or a narrow network profile blocks them
			// before the proxy is reached.
			AllowDomain: []nono.Domain{{Domain: "api.github.com"}},
			Credentials: []string{"github"},
			CustomCredentials: map[string]nono.CustomCredential{
				"github": {
					Upstream:      "https://api.github.com",
					CredentialKey: "env://" + tokenVar,
					EnvVar:        "GITHUB_TOKEN",
					InjectMode:    "header",
					InjectHeader:  "Authorization",
					CredentialFmt: "token {}",
				},
			},
		},
		// Only what the sandbox needs from the host. The token arrives by
		// injection, GH_CONFIG_DIR is set below, and the pipeline sets the
		// GIT_CONFIG_* entries, so none of them needs passing through.
		Environment: &nono.Environment{},
	}

	if *p.cfg.Git {
		// A second route is needed because git speaks to github.com, not to
		// the API host, and it authenticates with basic auth rather than a
		// bearer header. It reads the same variable, so the token is fetched
		// once.
		f.Network.AllowDomain = append(f.Network.AllowDomain, nono.Domain{Domain: "github.com"})
		f.Network.Credentials = append(f.Network.Credentials, "github_git")
		f.Network.CustomCredentials["github_git"] = nono.CustomCredential{
			Upstream:      "https://github.com",
			CredentialKey: "env://" + gitVar,
			EnvVar:        "GITHUB_GIT_AUTH",
			InjectMode:    "basic_auth",
		}
	}

	if *p.cfg.GHCLI {
		// No filesystem grant: this sits inside the artifact directory, which
		// the base profile already grants recursively.
		f.Environment.SetVars = map[string]string{"GH_CONFIG_DIR": workspace.ProfileVar + "/gh"}
	}

	secrets := []tool.Secret{{EnvVar: tokenVar, Key: p.cfg.Secret}}
	if *p.cfg.Git {
		// The same fnox key, read once and placed under a second name.
		// GitHub takes a token as the password for any user name.
		secrets = append(secrets, tool.Secret{EnvVar: gitVar, Key: p.cfg.Secret, Format: "x-access-token:{}"})
	}
	r := &tool.Result{Fragment: f, Secrets: secrets}
	if *p.cfg.Git && *p.cfg.RewriteSSH {
		r.GitConfig = sshRewrite()
	}
	return r, nil
}

// sshRewrite makes git use HTTPS for github.com even when a remote is written
// as ssh.
//
// The proxy can only add a credential to an HTTPS request, so an ssh remote
// would leave the sandbox with no way to authenticate, and the agent sees a
// permission error on the first fetch. Both spellings of an ssh remote are
// covered.
func sshRewrite() []tool.GitConfig {
	const key = "url.https://github.com/.insteadOf"
	return []tool.GitConfig{
		{Key: key, Value: "git@github.com:"},
		{Key: key, Value: "ssh://git@github.com/"},
	}
}
