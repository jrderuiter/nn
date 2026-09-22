// Package azuredevops grants Azure DevOps git-over-HTTPS and API access
// through nono's credential proxy, with a personal access token supplied by
// fnox.
package azuredevops

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/jrderuiter/nn/internal/nono"
	"github.com/jrderuiter/nn/internal/tool"
	"github.com/jrderuiter/nn/internal/workspace"
)

// Config is the [tools.azure_devops] table.
type Config struct {
	// Organization is the name in dev.azure.com/{organization}. A token
	// belongs to one organization, so only its remotes are rewritten.
	Organization string `toml:"organization" help:"organization name in dev.azure.com/{organization}"`
	// Secret is the fnox key that holds the personal access token.
	Secret string `toml:"secret" help:"fnox key holding the personal access token"`
	// Projects are rewritten to HTTPS on top of the ones that the remotes of
	// the repository name, so the agent can clone them over ssh too.
	Projects []string `toml:"projects" help:"extra projects whose ssh remotes use HTTPS"`
	// RewriteSSH turns an ssh remote into its HTTPS equivalent inside the
	// sandbox. Without it a repository cloned over ssh keeps using ssh, which
	// carries no credential the proxy can inject, so fetch fails.
	RewriteSSH *bool `toml:"rewrite_ssh" help:"use HTTPS for dev.azure.com remotes written as ssh"`
	// AzCLI redirects the az configuration directory into the project, so the
	// agent never touches the host az state.
	AzCLI *bool `toml:"az_cli" help:"redirect the az configuration into the project"`
}

type provider struct{ cfg Config }

func init() {
	// The prototype describes the shape, not the defaults: it is only read for
	// the field names that the environment can set.
	tool.Register("azure_devops", New, func() any { return &Config{} })
}

// New decodes the tool's own table.
func New(md toml.MetaData, prim toml.Primitive) (tool.Provider, error) {
	// The optional booleans start nil and default to true afterwards, for the
	// same reason as in the github tool: the decoder writes through an
	// existing pointer.
	cfg := Config{Secret: "AZURE_DEVOPS_PAT"}
	if err := md.PrimitiveDecode(prim, &cfg); err != nil {
		return nil, err
	}
	if cfg.Organization == "" {
		return nil, fmt.Errorf("organization must be set")
	}
	if cfg.Secret == "" {
		return nil, fmt.Errorf("secret must not be empty")
	}
	for _, f := range []**bool{&cfg.AzCLI, &cfg.RewriteSSH} {
		if *f == nil {
			v := true
			*f = &v
		}
	}
	return &provider{cfg}, nil
}

func (p *provider) Name() string { return "azure_devops" }

func (p *provider) Preflight(ctx context.Context, e *tool.Env) error {
	return e.Secrets.Check(ctx, p.cfg.Secret)
}

func (p *provider) Build(ctx context.Context, e *tool.Env) (*tool.Result, error) {
	// env:// rather than cmd://, so the token is resolved once, at launch.
	const tokenVar = "NN_AZURE_DEVOPS_PAT"

	f := &nono.Profile{
		Network: &nono.Network{
			AllowDomain: []nono.Domain{{Domain: "dev.azure.com"}},
			Credentials: []string{"azure_devops"},
			// One route is enough, unlike GitHub. Git and the REST API share
			// the host, and both take the token as a basic auth password. The
			// sandbox name is the one the az devops extension reads.
			CustomCredentials: map[string]nono.CustomCredential{
				"azure_devops": {
					Upstream:      "https://dev.azure.com",
					CredentialKey: "env://" + tokenVar,
					EnvVar:        "AZURE_DEVOPS_EXT_PAT",
					InjectMode:    "basic_auth",
				},
			},
		},
		Environment: &nono.Environment{},
	}

	if *p.cfg.AzCLI {
		// No filesystem grant: this sits inside the artifact directory, which
		// the base profile already grants recursively.
		f.Environment.SetVars = map[string]string{"AZURE_CONFIG_DIR": workspace.ProfileVar + "/az"}
	}

	r := &tool.Result{
		Fragment: f,
		Secrets:  []tool.Secret{{EnvVar: tokenVar, Key: p.cfg.Secret}},
	}
	if *p.cfg.RewriteSSH {
		projects, err := p.projects(ctx, e)
		if err != nil {
			return nil, err
		}
		r.GitConfig = sshRewrite(projects)
	}
	return r, nil
}

// project is one organization and project, spelled as the remote spells them.
// insteadOf matches case sensitively, so the spelling must be kept.
type project struct{ org, name string }

// projects lists the projects to rewrite: the ones that the remotes of the
// repository name, plus the configured ones. They come out sorted and without
// duplicates, so the profile does not depend on the order of the remotes.
func (p *provider) projects(ctx context.Context, e *tool.Env) ([]project, error) {
	seen := map[project]bool{}
	for _, name := range p.cfg.Projects {
		// A remote escapes a space as %20, so a configured name must too.
		seen[project{p.cfg.Organization, url.PathEscape(name)}] = true
	}
	if e.GitRemotes != nil {
		remotes, err := e.GitRemotes(ctx)
		if err != nil {
			return nil, fmt.Errorf("list git remotes: %w", err)
		}
		for _, r := range remotes {
			// Azure DevOps treats organization names without regard to case.
			if pr, ok := parseSSH(r); ok && strings.EqualFold(pr.org, p.cfg.Organization) {
				seen[pr] = true
			}
		}
	}
	out := make([]project, 0, len(seen))
	for pr := range seen {
		out = append(out, pr)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].org != out[j].org {
			return out[i].org < out[j].org
		}
		return out[i].name < out[j].name
	})
	return out, nil
}

// sshPrefixes are the two spellings of an Azure DevOps ssh remote.
var sshPrefixes = []string{"git@ssh.dev.azure.com:v3/", "ssh://git@ssh.dev.azure.com/v3/"}

// parseSSH reads the organization and project from an ssh remote such as
// git@ssh.dev.azure.com:v3/{org}/{project}/{repo}.
func parseSSH(remote string) (project, bool) {
	for _, prefix := range sshPrefixes {
		rest, ok := strings.CutPrefix(remote, prefix)
		if !ok {
			continue
		}
		parts := strings.Split(rest, "/")
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return project{}, false
		}
		return project{parts[0], parts[1]}, true
	}
	return project{}, false
}

// sshRewrite makes git use HTTPS for the given projects even when a remote is
// written as ssh.
//
// The HTTPS form puts _git between the project and the repository, and
// insteadOf only replaces a fixed prefix. So there is one rewrite per project,
// for each spelling of an ssh remote.
func sshRewrite(projects []project) []tool.GitConfig {
	var out []tool.GitConfig
	for _, pr := range projects {
		key := "url.https://dev.azure.com/" + pr.org + "/" + pr.name + "/_git/.insteadOf"
		for _, prefix := range sshPrefixes {
			out = append(out, tool.GitConfig{Key: key, Value: prefix + pr.org + "/" + pr.name + "/"})
		}
	}
	return out
}
