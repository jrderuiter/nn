package azuredevops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/jrderuiter/nn/internal/secrets"
	"github.com/jrderuiter/nn/internal/tool"
)

// acme sets both keys, for the tests that are not about inferring them.
const acme = "organization = \"acme\"\nproject = \"Platform\"\n"

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

func buildErr(t *testing.T, body string, remotes ...string) error {
	t.Helper()
	p, err := newProvider(body)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Build(context.Background(), &tool.Env{
		GitRemotes: func(context.Context) ([]string, error) { return remotes, nil },
	})
	return err
}

// There is no default organization or project. With no remote to infer them
// from, the user must set them, or the agent would work against a guess.
func TestWithoutRemotesBothKeysAreRequired(t *testing.T) {
	if err := buildErr(t, `project = "Platform"`); err == nil || !strings.Contains(err.Error(), "organization") {
		t.Fatalf("a missing organization must be an error, got %v", err)
	}
	if err := buildErr(t, `organization = "acme"`); err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("a missing project must be an error, got %v", err)
	}
}

// Remotes that disagree name no single value, so the user must choose.
func TestAmbiguousRemotesAreAnError(t *testing.T) {
	err := buildErr(t, `organization = "acme"`,
		"git@ssh.dev.azure.com:v3/acme/Platform/api",
		"https://dev.azure.com/acme/Web/_git/site",
	)
	if err == nil || !strings.Contains(err.Error(), "Platform, Web") {
		t.Fatalf("got %v", err)
	}
	if err := buildErr(t, "",
		"git@ssh.dev.azure.com:v3/acme/Platform/api",
		"git@ssh.dev.azure.com:v3/other/Platform/api",
	); err == nil || !strings.Contains(err.Error(), "organization") {
		t.Fatalf("got %v", err)
	}
}

// Only the az devops defaults use the project, so with az_cli off it is not
// needed.
func TestProjectIsOnlyNeededForAzCLI(t *testing.T) {
	if err := buildErr(t, "organization = \"acme\"\naz_cli = false\n"); err != nil {
		t.Fatal(err)
	}
}

func TestOneRouteCoversGitAndTheAPI(t *testing.T) {
	r := build(t, acme)
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
	vars := build(t, acme).Fragment.Environment.SetVars
	if vars["AZURE_CONFIG_DIR"] == "" {
		t.Fatal("the az configuration should be redirected by default")
	}
	// The SDK cache lives outside AZURE_CONFIG_DIR, in a host path that the
	// sandbox cannot write.
	if vars["AZURE_DEVOPS_CACHE_DIR"] == "" {
		t.Fatal("the Azure DevOps cache should be redirected by default")
	}
	if build(t, acme+"az_cli = false\n").Fragment.Environment.SetVars != nil {
		t.Fatal("az_cli = false should set nothing")
	}
}

func TestParseSSH(t *testing.T) {
	for _, c := range []struct {
		remote string
		want   project
		ok     bool
	}{
		{"git@ssh.dev.azure.com:v3/acme/Platform/api", project{"git@ssh.dev.azure.com:v3/", "acme", "Platform"}, true},
		{"ssh://git@ssh.dev.azure.com/v3/acme/Platform/api", project{"ssh://git@ssh.dev.azure.com/v3/", "acme", "Platform"}, true},
		{"git@ssh.dev.azure.com:v3/acme/Data%20Science/models", project{"git@ssh.dev.azure.com:v3/", "acme", "Data%20Science"}, true},
		// A host alias from an ssh configuration, which picks a key.
		{"git@team.ssh.dev.azure.com:v3/acme/Platform/api", project{"git@team.ssh.dev.azure.com:v3/", "acme", "Platform"}, true},
		{"git@evilssh.dev.azure.com:v3/acme/Platform/api", project{}, false},
		{"git@ssh.dev.azure.com:acme/Platform/api", project{}, false},
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
	r := build(t, acme+"projects = [\"Platform\", \"Data Science\"]\n",
		"git@ssh.dev.azure.com:v3/acme/Web/site",
		"ssh://git@ssh.dev.azure.com/v3/acme/Platform/api",
		"git@ssh.dev.azure.com:v3/other/Secret/repo",
		"git@github.com:acme/api.git",
	)
	var keys []string
	for _, e := range r.GitConfig {
		if len(keys) == 0 || keys[len(keys)-1] != e.Key {
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
	got := build(t, acme+"projects = [\"Platform\"]\n").GitConfig
	want := []tool.GitConfig{
		{Key: "url.https://dev.azure.com/acme/Platform/_git/.insteadOf", Value: "git@ssh.dev.azure.com:v3/acme/Platform/"},
		{Key: "url.https://dev.azure.com/acme/Platform/_git/.insteadOf", Value: "ssh://git@ssh.dev.azure.com/v3/acme/Platform/"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %+v", got)
	}
}

func TestRewriteCanBeTurnedOff(t *testing.T) {
	r := build(t, acme+"projects = [\"Platform\"]\nrewrite_ssh = false\n",
		"git@ssh.dev.azure.com:v3/acme/Web/site")
	if len(r.GitConfig) != 0 {
		t.Fatalf("rewrite_ssh = false should write no git configuration, got %+v", r.GitConfig)
	}
}

// nono base64-encodes a basic_auth value as it is, so the stored value must
// already be a user:password pair. A bare token reaches Azure DevOps as a
// malformed pair, and every request fails with 401.
func TestTokenIsStoredAsABasicAuthPair(t *testing.T) {
	if f := build(t, acme).Secrets[0].Format; f != ":{}" {
		t.Fatalf("got format %q", f)
	}
}

// insteadOf compares the literal start of a URL, so a remote that uses a host
// alias needs a rewrite in its own spelling.
func TestHostAliasKeepsItsSpelling(t *testing.T) {
	got := build(t, acme, "git@team.ssh.dev.azure.com:v3/acme/Platform/api").GitConfig
	want := "git@team.ssh.dev.azure.com:v3/acme/Platform/"
	for _, e := range got {
		if e.Value == want {
			if e.Key != "url.https://dev.azure.com/acme/Platform/_git/.insteadOf" {
				t.Fatalf("wrong target %q", e.Key)
			}
			return
		}
	}
	t.Fatalf("no rewrite for %q in %+v", want, got)
}

func defaults(t *testing.T, body string, remotes ...string) string {
	t.Helper()
	for _, a := range build(t, body, remotes...).Artifacts {
		if a.RelPath == "az/azuredevops/config" {
			return string(a.Content)
		}
	}
	t.Fatal("no az devops defaults file")
	return ""
}

// With the defaults file, az devops needs no --org or --project flag. Both
// values come from the remote when nn.toml does not set them.
func TestDefaultsNameTheOrganizationAndTheProject(t *testing.T) {
	got := defaults(t, "", "https://acme@dev.azure.com/acme/Data%20Science/_git/models")
	want := "[defaults]\norganization = https://dev.azure.com/acme\nproject = Data Science\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestConfiguredProjectWins(t *testing.T) {
	got := defaults(t, "organization = \"acme\"\nproject = \"Web\"\n", "git@ssh.dev.azure.com:v3/acme/Platform/api")
	if got != "[defaults]\norganization = https://dev.azure.com/acme\nproject = Web\n" {
		t.Fatalf("got %q", got)
	}
}

func TestAzCLIOffWritesNoDefaults(t *testing.T) {
	if a := build(t, acme+"az_cli = false\n").Artifacts; len(a) != 0 {
		t.Fatalf("got %+v", a)
	}
}

// Preflight asks fnox for the configured key, so doctor reports a PAT that
// does not resolve before an agent sees a 401.
func TestPreflightResolvesTheConfiguredSecret(t *testing.T) {
	fnox := filepath.Join(t.TempDir(), "fnox")
	script := "#!/bin/sh\nfor last; do :; done\n[ \"$last\" = ADO_PAT ] && echo pat && exit 0\necho \"no secret named $last\" >&2\nexit 1\n"
	if err := os.WriteFile(fnox, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := &tool.Env{Secrets: secrets.NewResolver(fnox, "", "")}
	for secret, ok := range map[string]bool{"ADO_PAT": true, "OTHER": false} {
		p, err := newProvider("organization = \"o\"\nproject = \"p\"\nsecret = \"" + secret + "\"\n")
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Preflight(context.Background(), env); (err == nil) != ok {
			t.Errorf("secret %s: got %v", secret, err)
		}
	}
}
