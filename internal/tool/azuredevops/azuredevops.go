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
	// belongs to one organization, so only its remotes are rewritten. Without
	// it, nn takes the organization from the remotes.
	Organization string `toml:"organization"`
	// Secret is the fnox key that holds the personal access token.
	Secret string `toml:"secret"`
	// Project is the default project for az devops. Without it, nn takes the
	// project from the remotes.
	Project string `toml:"project"`
	// Projects are rewritten to HTTPS on top of the ones that the remotes of
	// the repository name, so the agent can clone them over ssh too.
	Projects []string `toml:"projects"`
	// RewriteSSH turns an ssh remote into its HTTPS equivalent inside the
	// sandbox. Without it a repository cloned over ssh keeps using ssh, which
	// carries no credential the proxy can inject, so fetch fails.
	RewriteSSH *bool `toml:"rewrite_ssh"`
	// AzCLI redirects the az configuration directory into the project, so the
	// agent never touches the host az state.
	AzCLI *bool `toml:"az_cli"`
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
		// No filesystem grant: both sit inside the artifact directory, which
		// the base profile already grants recursively. The Azure DevOps SDK
		// keeps its cache in ~/.azure-devops rather than under
		// AZURE_CONFIG_DIR, and fails when it cannot write there.
		f.Environment.SetVars = map[string]string{
			"AZURE_CONFIG_DIR":       workspace.ProfileVar + "/az",
			"AZURE_DEVOPS_CACHE_DIR": workspace.ProfileVar + "/az/devops-cache",
		}
	}

	var remotes []string
	if e.GitRemotes != nil {
		var err error
		if remotes, err = e.GitRemotes(ctx); err != nil {
			return nil, fmt.Errorf("list git remotes: %w", err)
		}
	}

	r := &tool.Result{
		Fragment: f,
		// Azure DevOps ignores the user name, and documents an empty one.
		Secrets: []tool.Secret{{EnvVar: tokenVar, Key: p.cfg.Secret, Format: ":{}"}},
	}
	org, err := infer("organization", p.cfg.Organization, remotes, func(pr project) (string, bool) {
		return pr.org, true
	})
	if err != nil {
		return nil, err
	}
	if *p.cfg.AzCLI {
		proj, err := infer("project", p.cfg.Project, remotes, func(pr project) (string, bool) {
			return pr.name, strings.EqualFold(pr.org, org)
		})
		if err != nil {
			return nil, err
		}
		r.Artifacts = []tool.Artifact{{
			RelPath: "az/azuredevops/config",
			Mode:    0o644,
			Content: devopsDefaults(org, proj),
		}}
	}
	if *p.cfg.RewriteSSH {
		r.GitConfig = sshRewrite(p.projects(org, remotes))
	}
	return r, nil
}

// infer returns the configured value, or else the one value that the Azure
// DevOps remotes of the repository name. There is no default: with no such
// remote, or with remotes that disagree, any choice would be a guess, so the
// user has to set the key.
func infer(key, configured string, remotes []string, pick func(project) (string, bool)) (string, error) {
	if configured != "" {
		return configured, nil
	}
	// Azure DevOps treats organization and project names without regard to
	// case, so remotes that differ only in case agree.
	var found []string
	for _, r := range remotes {
		pr, ok := parseRemote(r)
		if !ok {
			continue
		}
		v, ok := pick(pr)
		if !ok {
			continue
		}
		dup := false
		for _, f := range found {
			dup = dup || strings.EqualFold(f, v)
		}
		if !dup {
			found = append(found, v)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", fmt.Errorf("%s is not set, and no Azure DevOps remote names one", key)
	default:
		sort.Strings(found)
		return "", fmt.Errorf("%s is not set, and the remotes name more than one: %s",
			key, strings.Join(found, ", "))
	}
}

// devopsDefaults is the file that `az devops configure --defaults` writes.
// With it, az devops commands need no --org or --project flag, so an agent
// does not have to find either value first.
func devopsDefaults(org, proj string) []byte {
	// The file holds the project as a person types it. A remote escapes a
	// space as %20.
	if unescaped, err := url.PathUnescape(proj); err == nil {
		proj = unescaped
	}
	return []byte("[defaults]\norganization = https://dev.azure.com/" + org + "\nproject = " + proj + "\n")
}

// project is one organization and project, spelled as the remote spells them.
// insteadOf matches case sensitively, so the spelling must be kept. from is
// the part of the remote before the organization, for example
// git@ssh.dev.azure.com:v3/. It varies with the host alias of the remote.
type project struct{ from, org, name string }

// projects lists the projects to rewrite: the ones that the remotes of the
// repository name, plus the configured ones. Each gets both default spellings
// of an ssh remote, and a remote also keeps its own. They come out sorted and
// without duplicates, so the profile does not depend on the order of the
// remotes.
func (p *provider) projects(org string, remotes []string) []project {
	seen := map[project]bool{}
	add := func(pr project) {
		seen[pr] = true
		for _, from := range sshPrefixes {
			seen[project{from, pr.org, pr.name}] = true
		}
	}
	for _, name := range p.cfg.Projects {
		// A remote escapes a space as %20, so a configured name must too.
		add(project{sshPrefixes[0], org, url.PathEscape(name)})
	}
	for _, r := range remotes {
		// Azure DevOps treats organization names without regard to case.
		if pr, ok := parseSSH(r); ok && strings.EqualFold(pr.org, org) {
			add(pr)
		}
	}
	out := make([]project, 0, len(seen))
	for pr := range seen {
		out = append(out, pr)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.org != b.org {
			return a.org < b.org
		}
		if a.name != b.name {
			return a.name < b.name
		}
		return a.from < b.from
	})
	return out
}

// sshPrefixes are the two default spellings of an Azure DevOps ssh remote.
var sshPrefixes = []string{"git@ssh.dev.azure.com:v3/", "ssh://git@ssh.dev.azure.com/v3/"}

// sshHost is the Azure DevOps ssh host. A remote may also name an alias that
// ends in it, such as team.ssh.dev.azure.com, which an ssh configuration
// uses to pick a key.
const sshHost = "ssh.dev.azure.com"

// parseSSH reads the organization and project from an ssh remote such as
// git@ssh.dev.azure.com:v3/{org}/{project}/{repo}.
func parseSSH(remote string) (project, bool) {
	var host, from, rest string
	if r, ok := strings.CutPrefix(remote, "ssh://git@"); ok {
		var path string
		host, path, ok = strings.Cut(r, "/")
		if rest, ok = strings.CutPrefix(path, "v3/"); !ok {
			return project{}, false
		}
		from = "ssh://git@" + host + "/v3/"
	} else if r, ok := strings.CutPrefix(remote, "git@"); ok {
		var path string
		host, path, ok = strings.Cut(r, ":")
		if rest, ok = strings.CutPrefix(path, "v3/"); !ok {
			return project{}, false
		}
		from = "git@" + host + ":v3/"
	} else {
		return project{}, false
	}
	if host != sshHost && !strings.HasSuffix(host, "."+sshHost) {
		return project{}, false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return project{}, false
	}
	return project{from, parts[0], parts[1]}, true
}

// parseRemote reads the organization and project from a remote in either the
// ssh form or the HTTPS form, https://dev.azure.com/{org}/{project}/_git/{repo}.
// The HTTPS form may carry a user name before the host.
func parseRemote(remote string) (project, bool) {
	if pr, ok := parseSSH(remote); ok {
		return pr, true
	}
	rest, ok := strings.CutPrefix(remote, "https://")
	if !ok {
		return project{}, false
	}
	if at := strings.Index(rest, "@"); at >= 0 && at < strings.Index(rest, "/") {
		rest = rest[at+1:]
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 5 || parts[0] != "dev.azure.com" || parts[3] != "_git" ||
		parts[1] == "" || parts[2] == "" || parts[4] == "" {
		return project{}, false
	}
	return project{"", parts[1], parts[2]}, true
}

// sshRewrite makes git use HTTPS for the given projects even when a remote is
// written as ssh.
//
// The HTTPS form puts _git between the project and the repository, and
// insteadOf only replaces a fixed prefix. So there is one rewrite per project,
// for each spelling of an ssh remote.
func sshRewrite(projects []project) []tool.GitConfig {
	out := make([]tool.GitConfig, 0, len(projects))
	for _, pr := range projects {
		out = append(out, tool.GitConfig{
			Key:   "url.https://dev.azure.com/" + pr.org + "/" + pr.name + "/_git/.insteadOf",
			Value: pr.from + pr.org + "/" + pr.name + "/",
		})
	}
	return out
}
