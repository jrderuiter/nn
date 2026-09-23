package kubernetes

import (
	"context"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/jrderuiter/nn/internal/tool"
)

// newFromTOML goes through the real decode path, which is where the
// exclusivity check lives.
func newFromTOML(t *testing.T, body string) (*provider, error) {
	t.Helper()
	var cfg struct {
		Tools map[string]toml.Primitive `toml:"tools"`
	}
	md, err := toml.Decode("[tools.kubernetes]\n"+body, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(md, cfg.Tools["kubernetes"])
	if err != nil {
		return nil, err
	}
	return p.(*provider), nil
}

// podEnv is a tool.Env whose workdir holds a mounted service account, so a
// relative service_account_dir finds it.
func podEnv(t *testing.T, namespace, account string) *tool.Env {
	t.Helper()
	dir := writePodDir(t, namespace, account)
	return &tool.Env{
		Workdir:     dir,
		ArtifactDir: dir + "/.nono/nn",
		HomeDir:     dir,
		Lookup:      func(string) (string, bool) { return "", false },
	}
}

// Each key that describes a kubeconfig is refused, rather than ignored, and the
// message names the way to drop it for one run.
func TestInClusterRejectsTheKeysItCannotHonour(t *testing.T) {
	cases := []struct {
		key  string
		body string
	}{
		{"context", `context = "prod-eks"`},
		{"kubeconfig", `kubeconfig = "other.yaml"`},
		{"cluster_ca", `cluster_ca = "ca.pem"`},
		{"allow_missing_ca", `allow_missing_ca = true`},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			_, err := newFromTOML(t, "auth = \"in_cluster\"\n"+c.body+"\n")
			if err == nil {
				t.Fatalf("%s must not be accepted with auth = in_cluster", c.key)
			}
			if !strings.Contains(err.Error(), c.key) {
				t.Fatalf("the error must name the key, got: %v", err)
			}
			want := "NN_TOOLS_KUBERNETES_" + strings.ToUpper(c.key) + "="
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("the error must name the escape %s, got: %v", want, err)
			}
		})
	}
}

// The escape the error names has to work, or one nn.toml cannot serve both a
// laptop and a pod.
func TestAnEmptyContextIsNotSetForTheCheck(t *testing.T) {
	p, err := newFromTOML(t, "auth = \"in_cluster\"\ncontext = \"\"\n")
	if err != nil {
		t.Fatalf("an empty context is what the pod spec sets, got: %v", err)
	}
	if !p.cfg.inCluster() {
		t.Fatal("the auth source must survive")
	}
}

func TestInClusterKeysNeedInCluster(t *testing.T) {
	for _, body := range []string{`api_server = "https://k8s.example.com"`, `service_account_dir = "sa"`} {
		if _, err := newFromTOML(t, body+"\n"); err == nil {
			t.Fatalf("%s must need auth = in_cluster", body)
		}
	}
}

// A misspelled source must fail here, because the alternative is a run that
// silently reads a kubeconfig the pod does not have.
func TestAnUnknownAuthIsRefused(t *testing.T) {
	_, err := newFromTOML(t, "auth = \"incluster\"\n")
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"incluster", "kubeconfig", "in_cluster"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the error must name %q, got: %v", want, err)
		}
	}
}

func TestInClusterDefaults(t *testing.T) {
	p, err := newFromTOML(t, "auth = \"in_cluster\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if p.cfg.APIServer != defaultAPIServer {
		t.Fatalf("api_server: got %q, want %q", p.cfg.APIServer, defaultAPIServer)
	}
	if p.saDir != defaultServiceAccountDir {
		t.Fatalf("service_account_dir: got %q", p.saDir)
	}
	// The namespace stays empty, so that the mounted one can win over it.
	if p.cfg.ServiceAccountNamespace != "" {
		t.Fatalf("namespace must not default in a pod, got %q", p.cfg.ServiceAccountNamespace)
	}
}

// Without a service account the pod's own token is used, read straight from
// the file the kubelet rotates.
func TestInClusterReadsTheMountedToken(t *testing.T) {
	p, err := newFromTOML(t, "auth = \"in_cluster\"\nservice_account_dir = \".\"\n")
	if err != nil {
		t.Fatal(err)
	}
	e := podEnv(t, "apps", "agent")
	res, err := p.Build(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	argv := res.Fragment.CredentialCapture[routeName].Command
	if argv[0] != "/bin/cat" || !strings.HasSuffix(argv[1], "/token") {
		t.Fatalf("the capture must read the mounted token, got %v", argv)
	}
	if ttl := res.Fragment.CredentialCapture[routeName].CacheTTLSecs; ttl != 60 {
		t.Fatalf("a rotated token needs a short cache, got %d", ttl)
	}
	if ns := p.cfg.ServiceAccountNamespace; ns != "apps" {
		t.Fatalf("the namespace comes from the mount, got %q", ns)
	}
	for _, a := range res.Artifacts {
		if a.RelPath == "kube/host.yaml" {
			t.Fatal("no host kubeconfig is needed when nothing is minted")
		}
	}
}

// An explicit namespace is about where an account lives, so it wins over the
// pod's own namespace.
func TestServiceAccountNamespaceWinsOverTheMountedOne(t *testing.T) {
	p, err := newFromTOML(t,
		"auth = \"in_cluster\"\nservice_account_dir = \".\"\nservice_account_namespace = \"other\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Build(context.Background(), podEnv(t, "apps", "agent")); err != nil {
		t.Fatal(err)
	}
	if got := p.cfg.ServiceAccountNamespace; got != "other" {
		t.Fatalf("got %q, want other", got)
	}
}

// Naming the account the pod already runs as changes nothing, so the same file
// works whether or not the pod spec happens to match it.
func TestInClusterSkipsMintingForItsOwnAccount(t *testing.T) {
	p, err := newFromTOML(t,
		"auth = \"in_cluster\"\nservice_account_dir = \".\"\nservice_account = \"agent\"\n")
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Build(context.Background(), podEnv(t, "apps", "agent"))
	if err != nil {
		t.Fatal(err)
	}
	if argv := res.Fragment.CredentialCapture[routeName].Command; argv[0] != "/bin/cat" {
		t.Fatalf("minting a token for the account we already are is pointless, got %v", argv)
	}
}

// A different account is a narrowing, so nn mints for it from inside the pod.
func TestInClusterMintsForADifferentAccount(t *testing.T) {
	p, err := newFromTOML(t, "auth = \"in_cluster\"\nservice_account_dir = \".\"\n"+
		"service_account = \"reader\"\nkubectl = \"/usr/local/bin/kubectl\"\n")
	if err != nil {
		t.Fatal(err)
	}
	e := podEnv(t, "apps", "agent")
	res, err := p.Build(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(res.Fragment.CredentialCapture[routeName].Command, " ")
	if !strings.Contains(argv, "create token reader -n apps") {
		t.Fatalf("got %s", argv)
	}
	if !strings.Contains(argv, "--kubeconfig "+e.ArtifactDir+"/kube/host.yaml") {
		t.Fatalf("the capture must name its own kubeconfig, got %s", argv)
	}

	var host []byte
	for _, a := range res.Artifacts {
		if a.RelPath == "kube/host.yaml" {
			host = a.Content
		}
	}
	if host == nil {
		t.Fatal("the kubeconfig the capture names must be written")
	}
	if !strings.Contains(string(host), "tokenFile:") {
		t.Fatalf("the host kubeconfig must present the mounted identity, got:\n%s", host)
	}
	if strings.Contains(string(host), "not-a-signature") {
		t.Fatalf("the host kubeconfig must hold no token value, got:\n%s", host)
	}
}

// The route verifies the API server with the authority the kubelet mounted,
// which is why neither cluster_ca nor allow_missing_ca is ever needed.
func TestInClusterVerifiesWithTheMountedAuthority(t *testing.T) {
	p, err := newFromTOML(t, "auth = \"in_cluster\"\nservice_account_dir = \".\"\n")
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Build(context.Background(), podEnv(t, "apps", "agent"))
	if err != nil {
		t.Fatal(err)
	}
	route := res.Fragment.Network.CustomCredentials[routeName]
	if route.Upstream != "https://kubernetes.default.svc" {
		t.Fatalf("upstream: got %q", route.Upstream)
	}
	if route.TLSCA != "$WORKDIR/.nono/nn/kube/ca.pem" {
		t.Fatalf("tls_ca: got %q", route.TLSCA)
	}
	var ca []byte
	for _, a := range res.Artifacts {
		if a.RelPath == "kube/ca.pem" {
			ca = a.Content
		}
	}
	if !strings.Contains(string(ca), "BEGIN CERTIFICATE") {
		t.Fatalf("the mounted authority must be written out, got %q", ca)
	}
	if domains := res.Fragment.Network.AllowDomain; len(domains) != 1 ||
		domains[0].Domain != "kubernetes.default.svc" {
		t.Fatalf("the API server must be reachable, got %v", domains)
	}
}

// The sandbox never sees a token, in a pod as anywhere else.
func TestInClusterKubeconfigCarriesNoSecret(t *testing.T) {
	p, err := newFromTOML(t, "auth = \"in_cluster\"\nservice_account_dir = \".\"\n")
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Build(context.Background(), podEnv(t, "apps", "agent"))
	if err != nil {
		t.Fatal(err)
	}
	var sandbox string
	for _, a := range res.Artifacts {
		if a.RelPath == "kube/config" {
			sandbox = string(a.Content)
		}
	}
	if strings.Contains(sandbox, "not-a-signature") || strings.Contains(sandbox, "tokenFile") {
		t.Fatalf("the sandbox kubeconfig must hold no credential, got:\n%s", sandbox)
	}
	if !strings.Contains(sandbox, tokenEnv) {
		t.Fatalf("the sandbox kubeconfig must present the phantom token, got:\n%s", sandbox)
	}
	if !strings.Contains(sandbox, "namespace: apps") {
		t.Fatalf("the sandbox must default to the pod's namespace, got:\n%s", sandbox)
	}
}

// auth = "in_cluster" on a machine that is not a pod has to say so plainly.
func TestInClusterOutsideAPodFails(t *testing.T) {
	p, err := newFromTOML(t, "auth = \"in_cluster\"\nservice_account_dir = \"nowhere\"\n")
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Build(context.Background(), podEnv(t, "apps", "agent"))
	if err == nil || !strings.Contains(err.Error(), "not a pod") {
		t.Fatalf("want a clear error, got: %v", err)
	}
}

func TestHostOnlyKeepsAnIPv6Address(t *testing.T) {
	cases := map[string]string{
		"kubernetes.default.svc":      "kubernetes.default.svc",
		"kubernetes.default.svc:6443": "kubernetes.default.svc",
		"[fd00::1]:443":               "fd00::1",
	}
	for in, want := range cases {
		if got := hostOnly(in); got != want {
			t.Fatalf("hostOnly(%q) = %q, want %q", in, got, want)
		}
	}
}

// The likely cause of an unreadable kubeconfig in a pod is the auth key left
// at its default,
// so the error says that instead of naming a path nobody meant to use.
func TestAMissingKubeconfigInAPodNamesTheSetting(t *testing.T) {
	dir := writePodDir(t, "apps", "agent")
	p := &provider{
		cfg:   Config{Kubeconfig: "absent.yaml", ServiceAccountNamespace: "default"},
		saDir: dir,
	}
	e := &tool.Env{
		Workdir: t.TempDir(),
		HomeDir: t.TempDir(),
		Lookup: func(k string) (string, bool) {
			if k == "KUBERNETES_SERVICE_HOST" {
				return "10.96.0.1", true
			}
			return "", false
		},
	}
	err := p.resolve(e)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "NN_TOOLS_KUBERNETES_AUTH=in_cluster") {
		t.Fatalf("the error must name the setting, got: %v", err)
	}
}

// Off a cluster the same failure must stay plain, or every typo would suggest
// a pod.
func TestAMissingKubeconfigOffAClusterStaysPlain(t *testing.T) {
	p := &provider{
		cfg:   Config{Kubeconfig: "absent.yaml", ServiceAccountNamespace: "default"},
		saDir: t.TempDir(),
	}
	e := &tool.Env{
		Workdir: t.TempDir(),
		HomeDir: t.TempDir(),
		Lookup:  func(string) (string, bool) { return "", false },
	}
	err := p.resolve(e)
	if err == nil || strings.Contains(err.Error(), "pod") {
		t.Fatalf("want a plain error, got: %v", err)
	}
}
