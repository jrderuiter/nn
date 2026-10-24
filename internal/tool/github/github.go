// Package github grants GitHub API and git-over-HTTPS access through nono's
// credential proxy, with a token from fnox or one that a GitHub App mints.
package github

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/jrderuiter/nn/internal/nono"
	"github.com/jrderuiter/nn/internal/tool"
	"github.com/jrderuiter/nn/internal/workspace"
)

// Config is the [tools.github] table.
type Config struct {
	// Secret is the fnox key that holds the token, or the App private key
	// when AppID is set.
	Secret string `toml:"secret"`
	// AppID switches to a GitHub App. nn then mints installation tokens from
	// the App private key, and nono asks for a new one before the old one
	// expires.
	AppID string `toml:"app_id"`
	// InstallationID names the App installation. When it is empty, the
	// github.com remote of the repository finds it.
	InstallationID string `toml:"installation_id"`
	// Repositories narrows an App token to these repository names.
	Repositories []string `toml:"repositories"`
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
	tool.RegisterCredential("github", credential)
}

// New decodes the tool's own table.
func New(md toml.MetaData, prim toml.Primitive) (tool.Provider, error) {
	// The optional booleans start nil and default to true afterwards. They
	// cannot be pre-filled: the decoder writes through an existing pointer, so
	// sharing one variable would let a single false value turn all of them off.
	var cfg Config
	if err := md.PrimitiveDecode(prim, &cfg); err != nil {
		return nil, err
	}
	if cfg.AppID == "" && (cfg.InstallationID != "" || len(cfg.Repositories) > 0) {
		return nil, errors.New("installation_id and repositories need app_id")
	}
	if cfg.Secret == "" {
		cfg.Secret = "GITHUB_TOKEN"
		if cfg.AppID != "" {
			cfg.Secret = "GITHUB_APP_PRIVATE_KEY"
		}
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
	if p.cfg.AppID != "" {
		if _, err := p.installation(ctx, e); err != nil {
			return err
		}
	}
	return e.Secrets.Check(ctx, p.cfg.Secret)
}

func (p *provider) Build(ctx context.Context, e *tool.Env) (*tool.Result, error) {
	// env:// rather than cmd://. nn resolves the token once, while the user is
	// starting the agent, and hands it to nono in its environment. A cmd://
	// capture would run fnox mid-session instead. An App token expires within
	// the hour, so appRoutes uses cmd:// with a capture that needs no fnox.
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

	secrets := []tool.Secret{{EnvVar: tokenVar, Key: p.cfg.Secret}}
	if *p.cfg.Git {
		// The same fnox key, read once and placed under a second name.
		// GitHub takes a token as the password for any user name.
		secrets = append(secrets, tool.Secret{EnvVar: gitVar, Key: p.cfg.Secret, Format: "x-access-token:{}"})
	}
	if p.cfg.AppID != "" {
		var err error
		if secrets, err = p.appRoutes(ctx, e, f); err != nil {
			return nil, err
		}
	}

	if *p.cfg.GHCLI {
		// No filesystem grant: this sits inside the artifact directory, which
		// the base profile already grants recursively.
		f.Environment.SetVars = map[string]string{"GH_CONFIG_DIR": workspace.ProfileVar + "/gh"}
	}

	r := &tool.Result{Fragment: f, Secrets: secrets}
	if *p.cfg.Git && *p.cfg.RewriteSSH {
		r.GitConfig = sshRewrite()
	}
	return r, nil
}

// appRoutes turns the routes of f into captures that mint App tokens. The App
// private key is the only secret: nn resolves it at launch, and nono hands it
// to each capture in its environment, so a new token never asks fnox again.
func (p *provider) appRoutes(ctx context.Context, e *tool.Env, f *nono.Profile) ([]tool.Secret, error) {
	target, err := p.installation(ctx, e)
	if err != nil {
		return nil, err
	}
	args := []string{e.Executable, "credential", "github", "--app-id", p.cfg.AppID}
	args = append(args, target...)
	for _, r := range p.cfg.Repositories {
		args = append(args, "--repository", r)
	}
	f.CredentialCapture = map[string]nono.CredentialCapture{}
	for _, name := range f.Network.Credentials {
		cmd := append([]string(nil), args...)
		if name == "github_git" {
			// basic_auth encodes the value as it is, so the capture must
			// print the user:password pair.
			cmd = append(cmd, "--git")
		}
		route := f.Network.CustomCredentials[name]
		route.CredentialKey = "cmd://" + name
		f.Network.CustomCredentials[name] = route
		f.CredentialCapture[name] = nono.CredentialCapture{
			Command:     cmd,
			TimeoutSecs: 30,
			// An installation token lasts an hour. Ten minutes of margin
			// keeps a cached token from expiring during a long request.
			CacheTTLSecs: 50 * 60,
		}
	}
	return []tool.Secret{{EnvVar: appKeyVar, Key: p.cfg.Secret}}, nil
}

// installation returns the capture arguments that find the App installation:
// the configured id, or else the one github.com repository of the remotes.
func (p *provider) installation(ctx context.Context, e *tool.Env) ([]string, error) {
	if p.cfg.InstallationID != "" {
		return []string{"--installation-id", p.cfg.InstallationID}, nil
	}
	var remotes []string
	if e.GitRemotes != nil {
		var err error
		if remotes, err = e.GitRemotes(ctx); err != nil {
			return nil, fmt.Errorf("list git remotes: %w", err)
		}
	}
	var found []string
	for _, r := range remotes {
		if repo, ok := parseRemote(r); ok && !containsFold(found, repo) {
			found = append(found, repo)
		}
	}
	switch len(found) {
	case 1:
		return []string{"--repo", found[0]}, nil
	case 0:
		return nil, errors.New("set installation_id, because no git remote names a github.com repository")
	default:
		// A fork has the App on one side only, and a guess would mint
		// for the wrong one or fail at the first request.
		return nil, fmt.Errorf("set installation_id, because the git remotes name more than one github.com repository: %s", strings.Join(found, ", "))
	}
}

// remotePattern matches the HTTPS and both ssh spellings of a github.com
// remote, and captures owner/name.
var remotePattern = regexp.MustCompile(`^(?:https://(?:[^@/]+@)?github\.com/|git@github\.com:|ssh://git@github\.com/)([A-Za-z0-9-]+/[A-Za-z0-9._-]+?)(?:\.git)?/?$`)

func parseRemote(remote string) (string, bool) {
	m := remotePattern.FindStringSubmatch(remote)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// containsFold compares without case, because GitHub does.
func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
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
