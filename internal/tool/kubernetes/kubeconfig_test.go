package kubernetes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

	"github.com/jrderuiter/nn/internal/tool"
)

const fixture = "testdata/kubeconfig.yaml"

func TestResolveNamedContext(t *testing.T) {
	kc, err := loadKubeconfig(fixture)
	if err != nil {
		t.Fatal(err)
	}
	r, err := kc.resolveContext("prod-eks")
	if err != nil {
		t.Fatal(err)
	}
	if r.Cluster.Server != "https://ABCDEF.gr7.eu-west-1.eks.amazonaws.com" {
		t.Fatalf("wrong server: %s", r.Cluster.Server)
	}
	if r.Namespace != "default" {
		t.Fatalf("wrong namespace: %s", r.Namespace)
	}
	if _, ok := r.User["exec"]; !ok {
		t.Fatal("the fixture user authenticates with an exec plugin")
	}
}

func TestResolveCurrentContextWhenNoneGiven(t *testing.T) {
	kc, _ := loadKubeconfig(fixture)
	r, err := kc.resolveContext("")
	if err != nil {
		t.Fatal(err)
	}
	if r.ContextName != "prod-eks" {
		t.Fatalf("expected the current context, got %s", r.ContextName)
	}
}

func TestResolveUnknownContextNamesTheAlternatives(t *testing.T) {
	kc, _ := loadKubeconfig(fixture)
	_, err := kc.resolveContext("nope")
	if err == nil {
		t.Fatal("an unknown context must be an error")
	}
	if !strings.Contains(err.Error(), "prod-eks") {
		t.Fatalf("the error should list the available contexts, got: %v", err)
	}
}

func TestCABytesDecodesInlineData(t *testing.T) {
	kc, _ := loadKubeconfig(fixture)
	r, _ := kc.resolveContext("prod-eks")
	ca, err := r.caBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(ca), "-----BEGIN CERTIFICATE-----") {
		t.Fatalf("expected a PEM certificate, got %q", string(ca))
	}
}

func TestCABytesIsEmptyWithoutACA(t *testing.T) {
	kc, _ := loadKubeconfig(fixture)
	r, _ := kc.resolveContext("local")
	ca, err := r.caBytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(ca) != 0 {
		t.Fatalf("a cluster without a CA must produce no file, got %d bytes", len(ca))
	}
}

// The proxy kubeconfig must carry no credential and no certificate authority.
// nono injects the token and checks the cluster certificate on the upstream
// leg, so the sandbox holds neither.
func TestProxyKubeconfigCarriesNoSecret(t *testing.T) {
	const server = "https://ABCDEF.gr7.eu-west-1.eks.amazonaws.com"
	out, err := proxyKubeconfig("prod-eks", server, "apps", "K8S_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	var kc kubeconfig
	if err := yaml.Unmarshal(out, &kc); err != nil {
		t.Fatal(err)
	}
	cl := kc.Clusters[0].Cluster
	if cl.Server != server {
		t.Fatalf("the client must talk to the real API server, got %s", cl.Server)
	}
	if cl.CertificateAuthority != "" || cl.CertificateAuthorityData != "" {
		t.Fatal("the proxy kubeconfig must not reference a certificate authority")
	}
	// Verification stays on. kubectl trusts nono's interception certificate
	// through the reusable authority in the user trust store.
	if cl.InsecureSkipTLSVerify {
		t.Fatal("verification must stay on; nono makes its own authority trusted")
	}
	if kc.Contexts[0].Context.Namespace != "apps" {
		t.Fatalf("wrong namespace: %s", kc.Contexts[0].Context.Namespace)
	}
	// An empty user makes kubectl prompt for a username on the first 401, so
	// the exec plugin has to be there, and it must read the phantom token
	// rather than embed anything.
	if _, ok := kc.Users[0].User["exec"]; !ok {
		t.Fatal("the user must carry an exec plugin, or kubectl prompts for a username")
	}
	if !strings.Contains(string(out), "K8S_TOKEN") {
		t.Fatal("the exec plugin must read the phantom token from the environment")
	}
	if strings.Contains(string(out), "certificate-authority") {
		t.Fatalf("the proxy kubeconfig must name no certificate authority:\n%s", out)
	}
}

func TestDirectKubeconfigKeepsTheHostUser(t *testing.T) {
	kc, _ := loadKubeconfig(fixture)
	r, _ := kc.resolveContext("local")
	out, err := directKubeconfig("local", r, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "local-static-token") {
		t.Fatal("the direct form carries the host credential, which is why it is the weaker option")
	}
}

// tls_ca takes a host path and expands only a leading tilde. A $WORKDIR
// spelling makes nono fail at launch with
// "Environment variable 'WORKDIR' validation failed: not set".
func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestProxyRouteUsesAnAbsoluteCAPath(t *testing.T) {
	p := &provider{
		cfg: Config{Context: "prod-eks", ServiceAccount: "ro", ServiceAccountNamespace: "apps",
			TokenTTL: "1h", Kubectl: "/usr/local/bin/kubectl", Kubeconfig: mustAbs(t, fixture)},
		ttl: time.Hour,
	}
	env := &tool.Env{
		Workdir: "/w", ArtifactDir: "/w/.nono/nn", HomeDir: "/h",
		Lookup: func(string) (string, bool) { return "", false },
	}
	r, err := p.Build(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	got := r.Fragment.Network.CustomCredentials["k8s"].TLSCA
	if got != "$WORKDIR/.nono/nn/kube/ca.pem" {
		t.Fatalf("tls_ca should stay relative to $WORKDIR, got %q", got)
	}
}

// nono runs a credential capture with a stripped environment, where a version
// manager shim cannot work. The failure surfaces only as
// "credential capture command failed with exit code 2", so nn refuses the shim
// up front with a message that says what to do.
func TestResolveKubectlRejectsAShim(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "shims")
	if err := os.MkdirAll(shim, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(shim, "kubectl")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !isShim(path) {
		t.Fatalf("%s should be recognised as a shim", path)
	}
	if !isShim("/Users/x/.local/share/mise/installs/fnox/latest/.mise-bins/kubectl") {
		t.Fatal("a .mise-bins path should be recognised as a shim")
	}
}

// An absolute path is taken as given, which is what keeps a generated profile
// the same on every machine.
func TestResolveKubectlTakesAnAbsolutePathAsGiven(t *testing.T) {
	got, err := resolveKubectl("/usr/local/bin/kubectl")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/usr/local/bin/kubectl" {
		t.Fatalf("got %q", got)
	}
}

// The command nono runs must name a binary by absolute path.
func TestTokenCommandUsesTheResolvedBinary(t *testing.T) {
	p := &provider{
		cfg: Config{Context: "prod-eks", ServiceAccount: "ro", ServiceAccountNamespace: "apps",
			TokenTTL: "1h", Kubectl: "kubectl"},
		kubectl: "/opt/homebrew/bin/kubectl",
		ttl:     time.Hour,
	}
	got := p.tokenCommand()
	want := []string{"/opt/homebrew/bin/kubectl", "--context", "prod-eks",
		"create", "token", "ro", "-n", "apps", "--duration=1h"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

// The service account namespace defaults, so a cluster whose account lives in
// the usual place needs one line less.
func TestServiceAccountNamespaceDefaults(t *testing.T) {
	var cfg struct {
		Tools map[string]toml.Primitive `toml:"tools"`
	}
	md, err := toml.Decode("[tools.kubernetes]\nauth = \"service-account\"\nservice_account = \"ro\"\n", &cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(md, cfg.Tools["kubernetes"])
	if err != nil {
		t.Fatal(err)
	}
	got := p.(*provider).cfg.ServiceAccountNamespace
	if got != "default" {
		t.Fatalf("got %q, want default", got)
	}
}

func TestServiceAccountNamespaceIsUsedForTheToken(t *testing.T) {
	p := &provider{
		cfg: Config{ServiceAccount: "ro", ServiceAccountNamespace: "agent-access",
			TokenTTL: "1h", Kubectl: "kubectl"},
		kubectl: "/opt/homebrew/bin/kubectl",
		ttl:     time.Hour,
	}
	got := strings.Join(p.tokenCommand(), " ")
	if !strings.Contains(got, "-n agent-access") {
		t.Fatalf("the token must be minted in the account's namespace, got %s", got)
	}
}

func newFromTOML(t *testing.T, body string) (tool.Provider, error) {
	t.Helper()
	var cfg struct {
		Tools map[string]toml.Primitive `toml:"tools"`
	}
	md, err := toml.Decode("[tools.kubernetes]\n"+body, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	return New(md, cfg.Tools["kubernetes"])
}

// The host form puts the context credentials in the sandbox, so a missing
// service account must never select it silently.
func TestAuthServiceAccountNeedsAnAccount(t *testing.T) {
	_, err := newFromTOML(t, "auth = \"service-account\"\ncontext = \"k3d-dev\"\n")
	if err == nil {
		t.Fatal("a missing service_account must be an error")
	}
	if !strings.Contains(err.Error(), `auth = "host"`) {
		t.Fatalf("the error should name the host form, got: %v", err)
	}
}

// auth has no default, so neither form is ever chosen by a missing key.
func TestAuthIsRequired(t *testing.T) {
	_, err := newFromTOML(t, "service_account = \"ro\"\n")
	if err == nil {
		t.Fatal("a missing auth must be an error, even with a service account")
	}
	if !strings.Contains(err.Error(), "auth is required") {
		t.Fatalf("got: %v", err)
	}
}

func TestAuthHostNeedsNoAccount(t *testing.T) {
	p, err := newFromTOML(t, "auth = \"host\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if p.(*provider).cfg.Auth != authHost {
		t.Fatalf("got auth %q", p.(*provider).cfg.Auth)
	}
}

func TestAuthHostRejectsAServiceAccount(t *testing.T) {
	if _, err := newFromTOML(t, "auth = \"host\"\nservice_account = \"ro\"\n"); err == nil {
		t.Fatal("auth = host together with service_account must be an error")
	}
}

func TestAuthRejectsAnUnknownValue(t *testing.T) {
	if _, err := newFromTOML(t, "auth = \"token\"\nservice_account = \"ro\"\n"); err == nil {
		t.Fatal("an unknown auth value must be an error")
	}
}

// kubectl reads the generated kubeconfig, and it expands no variable, so the
// $WORKDIR spelling of the profile turns into a path that does not exist.
// kubectl resolves a relative path against the directory of the kubeconfig,
// which is where ca.pem lands.
func TestDirectKubeconfigNamesTheCARelativeToItself(t *testing.T) {
	host := filepath.Join(t.TempDir(), "config")
	body := `apiVersion: v1
kind: Config
current-context: k3d
clusters:
  - name: k3d
    cluster:
      server: https://127.0.0.1:6443
      certificate-authority-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCmZha2UKLS0tLS1FTkQgQ0VSVElGSUNBVEUtLS0tLQo=
contexts:
  - name: k3d
    context:
      cluster: k3d
      user: k3d
users:
  - name: k3d
    user:
      token: local-static-token
`
	if err := os.WriteFile(host, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &provider{cfg: Config{Auth: authHost, Kubeconfig: host}}
	r, err := p.Build(context.Background(), &tool.Env{
		Workdir: "/w", ArtifactDir: "/w/.nono/nn", HomeDir: "/h",
		Lookup: func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatal(err)
	}
	var cfg, ca []byte
	for _, a := range r.Artifacts {
		switch a.RelPath {
		case "kube/config":
			cfg = a.Content
		case "kube/ca.pem":
			ca = a.Content
		}
	}
	if len(ca) == 0 {
		t.Fatal("the CA must be written next to the kubeconfig")
	}
	if !strings.Contains(string(cfg), "certificate-authority: ca.pem") || strings.Contains(string(cfg), "$") {
		t.Fatalf("the kubeconfig must name ca.pem relative to itself:\n%s", cfg)
	}
	// The fixture server is on this machine, so its port is opened.
	if n := r.Fragment.Network; len(n.OpenPort) != 1 || n.OpenPort[0] != 6443 || len(n.AllowDomain) != 0 {
		t.Fatalf("a local cluster needs its port opened, got %+v", n)
	}
}

// Go never sends a loopback address through a proxy, so a local cluster needs
// its port opened rather than its host allowed.
func TestLoopbackPort(t *testing.T) {
	for in, want := range map[string]int{
		"127.0.0.1:6550": 6550,
		"localhost:6443": 6443,
		"[::1]:6443":     6443,
		"0.0.0.0:6550":   6550,
		"127.0.0.1":      443,
	} {
		if got, ok := loopbackPort(in); !ok || got != want {
			t.Errorf("%s: got %d, %v, want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"ABCDEF.gr7.eu-west-1.eks.amazonaws.com", "10.0.0.5:6443", "k8s.example.com:6443"} {
		if _, ok := loopbackPort(in); ok {
			t.Errorf("%s is not on this machine", in)
		}
	}
}
