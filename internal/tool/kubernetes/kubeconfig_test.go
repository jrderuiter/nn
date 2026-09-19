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
		t.Fatal("verification must stay on; --trust-proxy-ca is what makes it work")
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
	got := p.tokenCommand("")
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
	md, err := toml.Decode("[tools.kubernetes]\nservice_account = \"ro\"\n", &cfg)
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
	got := strings.Join(p.tokenCommand(""), " ")
	if !strings.Contains(got, "-n agent-access") {
		t.Fatalf("the token must be minted in the account's namespace, got %s", got)
	}
}
