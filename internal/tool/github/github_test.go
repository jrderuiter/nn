package github

import (
	"context"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/jrderuiter/nn/internal/secrets"
	"github.com/jrderuiter/nn/internal/tool"
)

func build(t *testing.T, body string) *tool.Result {
	t.Helper()
	var cfg struct {
		Tools map[string]toml.Primitive `toml:"tools"`
	}
	md, err := toml.Decode("[tools.github]\n"+body, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(md, cfg.Tools["github"])
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Build(context.Background(), &tool.Env{
		Workdir: "/w", ArtifactDir: "/w/.nono/nn",
		Secrets: secrets.NewResolver("fnox", "", ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// An agent with API access but no git access cannot do the thing it was given
// the repository for, so git is on unless it is turned off.
func TestGitIsOnByDefault(t *testing.T) {
	r := build(t, "")
	if _, ok := r.Fragment.Network.CustomCredentials["github_git"]; !ok {
		t.Fatal("the git route should be there by default")
	}
	var found bool
	for _, c := range r.Fragment.Network.Credentials {
		if c == "github_git" {
			found = true
		}
	}
	if !found {
		// A route that is not listed here never fires, and nothing says so.
		t.Fatal("the git route must also be listed in network.credentials")
	}
}

func TestGitCanBeTurnedOff(t *testing.T) {
	r := build(t, "git = false\n")
	if _, ok := r.Fragment.Network.CustomCredentials["github_git"]; ok {
		t.Fatal("git = false should leave the git route out")
	}
	if v := r.Fragment.Environment.SetVars["GIT_CONFIG_COUNT"]; v != "" {
		t.Fatal("without the git route there is nothing to rewrite to")
	}
}

// The proxy can only add a credential to an HTTPS request, so an ssh remote
// has to be rewritten or the first fetch fails.
func TestSSHRemotesAreRewrittenToHTTPS(t *testing.T) {
	vars := build(t, "").Fragment.Environment.SetVars
	if vars["GIT_CONFIG_COUNT"] != "2" {
		t.Fatalf("expected two rewrites, got %q", vars["GIT_CONFIG_COUNT"])
	}
	var from []string
	for k, v := range vars {
		if strings.HasPrefix(k, "GIT_CONFIG_KEY_") && v != "url.https://github.com/.insteadOf" {
			t.Errorf("%s rewrites to the wrong place: %s", k, v)
		}
		if strings.HasPrefix(k, "GIT_CONFIG_VALUE_") {
			from = append(from, v)
		}
	}
	// Both spellings of an ssh remote have to be covered.
	want := map[string]bool{"git@github.com:": false, "ssh://git@github.com/": false}
	for _, f := range from {
		if _, ok := want[f]; !ok {
			t.Errorf("unexpected rewrite source %q", f)
		}
		want[f] = true
	}
	for f, seen := range want {
		if !seen {
			t.Errorf("no rewrite for %q", f)
		}
	}
}

func TestRewriteCanBeTurnedOff(t *testing.T) {
	vars := build(t, "rewrite_ssh = false\n").Fragment.Environment.SetVars
	if vars["GIT_CONFIG_COUNT"] != "" {
		t.Fatal("rewrite_ssh = false should write no git configuration")
	}
	if vars["GH_CONFIG_DIR"] == "" {
		t.Fatal("turning the rewrite off must not disturb the rest")
	}
}

// The token is resolved once, before the sandbox starts, and read from the
// environment. A cmd:// capture would run fnox mid-session and train the user
// to approve a secret whenever the agent asks.
func TestTokenIsResolvedUpFront(t *testing.T) {
	r := build(t, "")
	if len(r.Fragment.CredentialCapture) != 0 {
		t.Fatalf("no capture should remain, got %v", r.Fragment.CredentialCapture)
	}
	for _, s := range r.Secrets {
		if s.Key != "GITHUB_TOKEN" {
			t.Errorf("every secret comes from the configured key, got %+v", s)
		}
	}
}

// Each route needs its own credential. Sharing one binds both to a single
// broker credential, and the basic-auth route resolves a phantom into a
// user:token pair, which is not what the API route's caller sends.
func TestEachRouteHasItsOwnCredential(t *testing.T) {
	r := build(t, "")
	seen := map[string]string{}
	for name, route := range r.Fragment.Network.CustomCredentials {
		if prev, dup := seen[route.CredentialKey]; dup {
			t.Fatalf("routes %q and %q share %q", prev, name, route.CredentialKey)
		}
		seen[route.CredentialKey] = name
	}
	if len(r.Secrets) != 2 {
		t.Fatalf("expected one host variable per route, got %+v", r.Secrets)
	}
}

// The name nn uses on the host must not be the one the sandbox sees, or a
// phantom token and a real one would share a name.
func TestHostVariableDiffersFromTheSandboxOne(t *testing.T) {
	r := build(t, "")
	host := r.Secrets[0].EnvVar
	for name, route := range r.Fragment.Network.CustomCredentials {
		if route.EnvVar == host {
			t.Errorf("route %q hands the sandbox the same name as the host value", name)
		}
	}
}

// A credential route only injects a credential. The host still has to be in
// the allowlist, or a narrow network profile blocks it first.
func TestBothHostsAreAllowed(t *testing.T) {
	got := map[string]bool{}
	for _, d := range build(t, "").Fragment.Network.AllowDomain {
		got[d.Domain] = true
	}
	for _, want := range []string{"api.github.com", "github.com"} {
		if !got[want] {
			t.Errorf("%s is not in the allowlist", want)
		}
	}
}

func TestGitOffAllowsOnlyTheAPIHost(t *testing.T) {
	doms := build(t, "git = false\n").Fragment.Network.AllowDomain
	if len(doms) != 1 || doms[0].Domain != "api.github.com" {
		t.Fatalf("got %+v", doms)
	}
}
