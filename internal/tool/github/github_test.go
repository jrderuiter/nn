package github

import (
	"context"
	"fmt"
	"testing"

	"github.com/BurntSushi/toml"

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
	if len(r.GitConfig) != 0 {
		t.Fatal("without the git route there is nothing to rewrite to")
	}
}

// The proxy can only add a credential to an HTTPS request, so an ssh remote
// has to be rewritten or the first fetch fails.
func TestSSHRemotesAreRewrittenToHTTPS(t *testing.T) {
	entries := build(t, "").GitConfig
	if len(entries) != 2 {
		t.Fatalf("expected two rewrites, got %+v", entries)
	}
	// Both spellings of an ssh remote have to be covered.
	want := map[string]bool{"git@github.com:": false, "ssh://git@github.com/": false}
	for _, e := range entries {
		if e.Key != "url.https://github.com/.insteadOf" {
			t.Errorf("%q rewrites to the wrong place: %s", e.Value, e.Key)
		}
		if _, ok := want[e.Value]; !ok {
			t.Errorf("unexpected rewrite source %q", e.Value)
		}
		want[e.Value] = true
	}
	for f, seen := range want {
		if !seen {
			t.Errorf("no rewrite for %q", f)
		}
	}
}

func TestRewriteCanBeTurnedOff(t *testing.T) {
	r := build(t, "rewrite_ssh = false\n")
	if len(r.GitConfig) != 0 {
		t.Fatal("rewrite_ssh = false should write no git configuration")
	}
	if r.Fragment.Environment.SetVars["GH_CONFIG_DIR"] == "" {
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

// nono base64-encodes a basic_auth value as it is, so the git route needs a
// user:token pair, while the header route needs the bare token.
func TestGitRouteStoresABasicAuthPair(t *testing.T) {
	for _, s := range build(t, "").Secrets {
		want := ""
		if s.EnvVar == "NN_GITHUB_GIT_AUTH" {
			want = "x-access-token:{}"
		}
		if s.Format != want {
			t.Errorf("%s has format %q, want %q", s.EnvVar, s.Format, want)
		}
	}
}

// Preflight asks for the configured key, so doctor reports a token that does
// not resolve before an agent sees a 401.
func TestPreflightChecksTheConfiguredSecret(t *testing.T) {
	env := &tool.Env{Secrets: fakeSecrets{"GH_TOKEN": true}}
	for secret, ok := range map[string]bool{"GH_TOKEN": true, "OTHER": false} {
		var cfg struct {
			Tools map[string]toml.Primitive `toml:"tools"`
		}
		md, err := toml.Decode("[tools.github]\nsecret = \""+secret+"\"\n", &cfg)
		if err != nil {
			t.Fatal(err)
		}
		p, err := New(md, cfg.Tools["github"])
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Preflight(context.Background(), env); (err == nil) != ok {
			t.Errorf("secret %s: got %v", secret, err)
		}
	}
}

// fakeSecrets resolves only the keys it holds.
type fakeSecrets map[string]bool

func (f fakeSecrets) Check(_ context.Context, key string) error {
	if !f[key] {
		return fmt.Errorf("no secret named %s", key)
	}
	return nil
}
