package azuredevops

import (
	"context"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/jrderuiter/nn/internal/secrets"
	"github.com/jrderuiter/nn/internal/tool"
)

func newProvider(body string) (tool.Provider, error) {
	var cfg struct {
		Tools map[string]toml.Primitive `toml:"tools"`
	}
	md, err := toml.Decode("[tools.azure_devops]\n"+body, &cfg)
	if err != nil {
		return nil, err
	}
	return New(md, cfg.Tools["azure_devops"])
}

func build(t *testing.T, body string, remotes ...string) *tool.Result {
	t.Helper()
	p, err := newProvider(body)
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Build(context.Background(), &tool.Env{
		Workdir: "/w", ArtifactDir: "/w/.nono/nn",
		Secrets:    secrets.NewResolver("fnox", "", ""),
		GitRemotes: func(context.Context) ([]string, error) { return remotes, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// A token belongs to one organization, so there is nothing to grant without
// one.
func TestOrganizationIsRequired(t *testing.T) {
	if _, err := newProvider(""); err == nil {
		t.Fatal("a missing organization must be an error")
	}
}

func TestOneRouteCoversGitAndTheAPI(t *testing.T) {
	r := build(t, `organization = "acme"`)
	n := r.Fragment.Network
	if len(n.AllowDomain) != 1 || n.AllowDomain[0].Domain != "dev.azure.com" {
		t.Fatalf("got %+v", n.AllowDomain)
	}
	route, ok := n.CustomCredentials["azure_devops"]
	if !ok || len(n.Credentials) != 1 || n.Credentials[0] != "azure_devops" {
		// A route that is not listed in network.credentials never fires.
		t.Fatalf("the route must be defined and listed: %+v", n)
	}
	if route.InjectMode != "basic_auth" || route.EnvVar != "AZURE_DEVOPS_EXT_PAT" {
		t.Fatalf("got %+v", route)
	}
	if len(r.Secrets) != 1 || r.Secrets[0].Key != "AZURE_DEVOPS_PAT" ||
		route.CredentialKey != "env://"+r.Secrets[0].EnvVar {
		t.Fatalf("the route must read the resolved secret: %+v %+v", route, r.Secrets)
	}
}

func TestAzCLICanBeTurnedOff(t *testing.T) {
	if build(t, `organization = "acme"`).Fragment.Environment.SetVars["AZURE_CONFIG_DIR"] == "" {
		t.Fatal("the az configuration should be redirected by default")
	}
	if build(t, "organization = \"acme\"\naz_cli = false\n").Fragment.Environment.SetVars != nil {
		t.Fatal("az_cli = false should set nothing")
	}
}

func TestParseSSH(t *testing.T) {
	for _, c := range []struct {
		remote string
		want   project
		ok     bool
	}{
		{"git@ssh.dev.azure.com:v3/acme/Platform/api", project{"acme", "Platform"}, true},
		{"ssh://git@ssh.dev.azure.com/v3/acme/Platform/api", project{"acme", "Platform"}, true},
		{"git@ssh.dev.azure.com:v3/acme/Data%20Science/models", project{"acme", "Data%20Science"}, true},
		{"https://dev.azure.com/acme/Platform/_git/api", project{}, false},
		{"git@github.com:acme/api.git", project{}, false},
		{"git@ssh.dev.azure.com:v3/acme/Platform", project{}, false},
	} {
		got, ok := parseSSH(c.remote)
		if ok != c.ok || got != c.want {
			t.Errorf("parseSSH(%q) = %+v, %v; want %+v, %v", c.remote, got, ok, c.want, c.ok)
		}
	}
}

// The remotes and the configured projects merge into one sorted list, so the
// profile does not depend on the order in which git lists the remotes.
func TestProjectsComeFromRemotesAndConfiguration(t *testing.T) {
	r := build(t, "organization = \"acme\"\nprojects = [\"Platform\", \"Data Science\"]\n",
		"git@ssh.dev.azure.com:v3/acme/Web/site",
		"ssh://git@ssh.dev.azure.com/v3/acme/Platform/api",
		"git@ssh.dev.azure.com:v3/other/Secret/repo",
		"git@github.com:acme/api.git",
	)
	var keys []string
	for i, e := range r.GitConfig {
		if i%2 == 0 {
			keys = append(keys, e.Key)
		}
	}
	want := []string{
		"url.https://dev.azure.com/acme/Data%20Science/_git/.insteadOf",
		"url.https://dev.azure.com/acme/Platform/_git/.insteadOf",
		"url.https://dev.azure.com/acme/Web/_git/.insteadOf",
	}
	if len(keys) != len(want) {
		t.Fatalf("got %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Errorf("rewrite %d is %q, want %q", i, keys[i], want[i])
		}
	}
}

// Both spellings of an ssh remote have to be covered.
func TestBothSSHSpellingsAreRewritten(t *testing.T) {
	got := build(t, "organization = \"acme\"\nprojects = [\"Platform\"]\n").GitConfig
	want := []tool.GitConfig{
		{Key: "url.https://dev.azure.com/acme/Platform/_git/.insteadOf", Value: "git@ssh.dev.azure.com:v3/acme/Platform/"},
		{Key: "url.https://dev.azure.com/acme/Platform/_git/.insteadOf", Value: "ssh://git@ssh.dev.azure.com/v3/acme/Platform/"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %+v", got)
	}
}

func TestRewriteCanBeTurnedOff(t *testing.T) {
	r := build(t, "organization = \"acme\"\nprojects = [\"Platform\"]\nrewrite_ssh = false\n",
		"git@ssh.dev.azure.com:v3/acme/Web/site")
	if len(r.GitConfig) != 0 {
		t.Fatalf("rewrite_ssh = false should write no git configuration, got %+v", r.GitConfig)
	}
}

// nono base64-encodes a basic_auth value as it is, so the stored value must
// already be a user:password pair. A bare token reaches Azure DevOps as a
// malformed pair, and every request fails with 401.
func TestTokenIsStoredAsABasicAuthPair(t *testing.T) {
	if f := build(t, `organization = "acme"`).Secrets[0].Format; f != ":{}" {
		t.Fatalf("got format %q", f)
	}
}
