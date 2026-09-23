// Package kubernetes gives a sandboxed agent access to one Kubernetes cluster.
//
// nono has no Kubernetes feature of its own, so this provider assembles the
// access out of two generic nono parts: a credential_capture entry that mints a
// token on the host, and a custom_credentials proxy route that injects it. The
// token never enters the sandbox.
package kubernetes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/jrderuiter/nn/internal/nono"
	"github.com/jrderuiter/nn/internal/tool"
	"github.com/jrderuiter/nn/internal/workspace"
)

// Config is the [tools.kubernetes] table.
type Config struct {
	// Context names the kubeconfig context to use. Empty means the current one.
	Context string `toml:"context" help:"kubeconfig context to use, defaulting to the current one"`
	// ServiceAccount turns on proxy mode. nono mints a token for this existing
	// service account on the host. nn never creates accounts or RBAC.
	ServiceAccount string `toml:"service_account" help:"existing service account to mint a token for; turns on proxy mode"`
	// ServiceAccountNamespace is where the service account lives, defaulting to
	// "default". It is also the default namespace in the generated kubeconfig.
	ServiceAccountNamespace string `toml:"service_account_namespace" help:"namespace the service account lives in, and the default namespace in the sandbox"`
	// TokenTTL is how long a minted token stays valid, for example "1h".
	TokenTTL string `toml:"token_ttl" help:"how long a minted token stays valid, for example 1h"`
	// Kubectl is the binary used to mint tokens on the host.
	Kubectl string `toml:"kubectl" help:"kubectl binary used on the host; a real path, not a version manager shim"`
	// Kubeconfig overrides the host kubeconfig path.
	Kubeconfig string `toml:"kubeconfig" help:"host kubeconfig to read, defaulting to KUBECONFIG or ~/.kube/config"`
	// ClusterCA points at a PEM file holding the cluster's certificate
	// authority. Set it when the kubeconfig context does not carry one.
	ClusterCA string `toml:"cluster_ca" help:"PEM file with the cluster certificate authority, when the context carries none"`
	// AllowMissingCA turns off nn's own check that a certificate authority
	// was found. nono has no option to skip upstream verification, so this
	// only helps when the API server uses a publicly trusted certificate.
	AllowMissingCA bool `toml:"allow_missing_ca" help:"do not require a cluster certificate authority"`
	// Auth names where the credentials come from: a kubeconfig on the host, or
	// the identity the kubelet mounted into this pod. nn never detects the pod
	// on its own: a guess would make one command mean different things in
	// different places.
	Auth string `toml:"auth" help:"where credentials come from: kubeconfig or in_cluster"`
	// APIServer overrides the in-cluster address of the API server.
	APIServer string `toml:"api_server" help:"in-cluster API server address, defaulting to https://kubernetes.default.svc"`
	// ServiceAccountDir is where the pod identity is mounted.
	ServiceAccountDir string `toml:"service_account_dir" help:"directory holding the mounted service account, for auth = \"in_cluster\""`
}

const routeName = "k8s"

// The values of the auth key. They name the source of the credentials, not the
// identity: service_account picks the identity, in either source.
const (
	authKubeconfig = "kubeconfig"
	authInCluster  = "in_cluster"
)

// inCluster reports whether the credentials come from the mounted pod identity.
func (c *Config) inCluster() bool { return c.Auth == authInCluster }

// tokenEnv is the variable that carries the per-session phantom token into the
// sandbox. Nothing reads it: the proxy injects the real token. nono requires
// the field whenever credential_key uses the cmd:// scheme.
const tokenEnv = "K8S_TOKEN"

type provider struct {
	cfg     Config
	ttl     time.Duration
	host    string // resolved API server, filled by resolve
	kubectl string // absolute kubectl path, filled by resolve
	// kubeconfig is the resolved host kubeconfig, set only when the
	// configuration named one.
	kubeconfig string
	workdir    string
	res        *resolved
	// saDir is where a mounted service account would be. The in-cluster mode
	// reads it; the kubeconfig mode only asks whether it is there, to tell a
	// missing kubeconfig apart from a pod nobody configured.
	saDir string
	// pod is the mounted identity, set only in the in-cluster mode.
	pod *podIdentity
	// hostKubeconfig is the kubeconfig that the capture command uses to mint a
	// token from inside the pod. It is empty unless the in-cluster mode mints.
	hostKubeconfig []byte
}

// mints reports whether the token comes from `kubectl create token` rather
// than from a file the kubelet rotates.
func (p *provider) mints() bool {
	if p.cfg.ServiceAccount == "" {
		return false
	}
	if !p.cfg.inCluster() {
		return true
	}
	// Minting for the account the pod already runs as would need RBAC to gain
	// nothing, so the mounted token stands.
	return p.pod == nil || p.cfg.ServiceAccount != p.pod.ServiceAccount
}

func init() {
	// The prototype describes the shape, not the defaults: it is only read for
	// the field names the environment can set.
	tool.Register("kubernetes", New, func() any { return &Config{} })
}

func New(md toml.MetaData, prim toml.Primitive) (tool.Provider, error) {
	// The two mode specific keys carry no default here, so that an unset key
	// stays distinguishable from one the user wrote. checkExclusive reads that
	// difference, and the defaults are applied below.
	cfg := Config{TokenTTL: "1h", Kubectl: "kubectl"}
	if err := md.PrimitiveDecode(prim, &cfg); err != nil {
		return nil, err
	}
	ttl, err := time.ParseDuration(cfg.TokenTTL)
	if err != nil {
		return nil, fmt.Errorf("token_ttl %q is not a duration: %w", cfg.TokenTTL, err)
	}
	if ttl < time.Minute {
		return nil, fmt.Errorf("token_ttl must be at least one minute, got %s", cfg.TokenTTL)
	}
	switch cfg.Auth {
	case "":
		cfg.Auth = authKubeconfig
	case authKubeconfig, authInCluster:
	default:
		return nil, fmt.Errorf("auth %q is not a source of credentials; it is %q or %q",
			cfg.Auth, authKubeconfig, authInCluster)
	}
	if err := checkExclusive(&cfg); err != nil {
		return nil, err
	}
	if cfg.inCluster() {
		if cfg.APIServer == "" {
			cfg.APIServer = defaultAPIServer
		}
	} else if cfg.ServiceAccountNamespace == "" {
		// In a pod the namespace comes from the mounted file instead, which
		// resolve fills in.
		cfg.ServiceAccountNamespace = "default"
	}
	saDir := cfg.ServiceAccountDir
	if saDir == "" {
		saDir = defaultServiceAccountDir
	}
	return &provider{cfg: cfg, ttl: ttl, saDir: saDir}, nil
}

// checkExclusive rejects a key that cannot apply in the chosen mode.
//
// nn could ignore such a key instead. It does not, because a configuration that
// names a context is a statement about which cluster the agent reaches, and
// silently dropping it would hand the agent a different cluster than the file
// describes.
func checkExclusive(cfg *Config) error {
	if !cfg.inCluster() {
		for _, k := range []struct {
			name string
			set  bool
		}{
			{"api_server", cfg.APIServer != ""},
			{"service_account_dir", cfg.ServiceAccountDir != ""},
		} {
			if k.set {
				return fmt.Errorf("%s only applies with auth = %q, and auth is %q.\n"+
					"  Change auth, or set %s= with an empty value for this run",
					k.name, authInCluster, cfg.Auth, envVarFor(k.name))
			}
		}
		return nil
	}
	// The order is fixed so the message is the same on every run.
	for _, k := range []struct {
		name string
		set  bool
		why  string
	}{
		{"context", cfg.Context != "", "a pod has no kubeconfig to take a context from"},
		{"kubeconfig", cfg.Kubeconfig != "", "this source reads no kubeconfig"},
		{"cluster_ca", cfg.ClusterCA != "", "the mounted ca.crt is the cluster's own authority"},
		{"allow_missing_ca", cfg.AllowMissingCA, "the authority is always mounted in a pod"},
	} {
		if !k.set {
			continue
		}
		return fmt.Errorf("auth is %q, so %s has no meaning: %s.\n"+
			"  Remove %s from the configuration, or set %s= with an empty value for this run",
			authInCluster, k.name, k.why, k.name, envVarFor(k.name))
	}
	return nil
}

// envVarFor names the variable that overrides one key, which is the escape a
// pod spec uses to drop a value that a committed nn.toml sets.
func envVarFor(key string) string {
	return "NN_TOOLS_KUBERNETES_" + strings.ToUpper(key)
}

func (p *provider) Name() string { return "kubernetes" }

// resolve works out everything Build needs. It reads files but changes
// nothing, so both Preflight and Build can call it.
func (p *provider) resolve(e *tool.Env) error {
	if p.cfg.inCluster() {
		return p.resolveInCluster(e)
	}
	path := p.cfg.Kubeconfig
	switch {
	case path == "":
		path = hostKubeconfigPath(e.HomeDir, e.Lookup)
	case !filepath.IsAbs(path):
		// A relative path in nn.toml means relative to the project, not to
		// whatever directory the user happened to launch nn from.
		path = filepath.Join(e.Workdir, path)
	}
	if _, err := os.Stat(path); err != nil {
		if looksLikePod(e.Lookup, p.saDir) {
			return fmt.Errorf("kubeconfig %s is not readable, and this looks like a pod.\n"+
				"  Set auth = %q, or NN_TOOLS_KUBERNETES_AUTH=%s in the pod spec",
				path, authInCluster, authInCluster)
		}
		return fmt.Errorf("kubeconfig %s is not readable: %w", path, err)
	}
	kc, err := loadKubeconfig(path)
	if err != nil {
		return err
	}
	res, err := kc.resolveContext(p.cfg.Context)
	if err != nil {
		return err
	}
	u, err := url.Parse(res.Cluster.Server)
	if err != nil || u.Host == "" {
		return fmt.Errorf("context %q has an unusable server URL %q", res.ContextName, res.Cluster.Server)
	}
	p.res = res
	p.host = u.Host
	p.workdir = e.Workdir
	if p.cfg.Kubeconfig != "" {
		p.kubeconfig = path
	}

	if p.cfg.ServiceAccount == "" {
		return nil
	}
	p.kubectl, err = resolveKubectl(p.cfg.Kubectl)
	if err != nil {
		return err
	}
	return p.checkCA()
}

// resolveInCluster fills the same fields the kubeconfig path fills, from the
// identity the kubelet mounted, so that everything downstream stays the same.
func (p *provider) resolveInCluster(e *tool.Env) error {
	dir := p.saDir
	if !filepath.IsAbs(dir) {
		// A relative path means relative to the project, as it does for
		// kubeconfig. Nothing needs it in a real pod, where the mount point is
		// absolute, but it is what lets a test describe a whole pod identity.
		dir = filepath.Join(e.Workdir, dir)
	}
	id, err := readPodIdentity(dir)
	if err != nil {
		return err
	}
	u, err := url.Parse(p.cfg.APIServer)
	if err != nil || u.Host == "" {
		return fmt.Errorf("api_server %q is not a usable URL", p.cfg.APIServer)
	}
	ns := p.cfg.ServiceAccountNamespace
	if ns == "" {
		ns = id.Namespace
	}
	p.pod = id
	p.workdir = e.Workdir
	p.host = u.Host
	// The mounted ca.crt goes in as a file path, which is a form the kubeconfig
	// reader already understands, so caBytes and checkCA need no change.
	p.res = &resolved{
		ContextName: "in-cluster",
		Cluster:     cluster{Server: p.cfg.APIServer, CertificateAuthority: id.CAFile},
		Namespace:   ns,
	}
	// The namespace is also what the generated kubeconfig and any minted token
	// use, and both read it from the configuration.
	p.cfg.ServiceAccountNamespace = ns

	if !p.mints() {
		return p.checkCA()
	}
	if p.kubectl, err = resolveKubectl(p.cfg.Kubectl); err != nil {
		return err
	}
	if p.hostKubeconfig, err = podKubeconfig(p.cfg.APIServer, id); err != nil {
		return err
	}
	return p.checkCA()
}

// checkCA makes sure the proxy will be able to verify the API server.
//
// Without a certificate authority the proxy falls back to the system roots,
// and a cluster with a private authority then fails at the first request with
// "TLS handshake failed: invalid peer certificate: UnknownIssuer".
func (p *provider) checkCA() error {
	if p.cfg.AllowMissingCA {
		return nil
	}
	ca, err := p.caBytes()
	if err != nil {
		return err
	}
	if len(ca) > 0 {
		if !bytes.Contains(ca, []byte("BEGIN CERTIFICATE")) {
			return fmt.Errorf("the certificate authority for context %q is not PEM encoded", p.res.ContextName)
		}
		return nil
	}
	return fmt.Errorf("context %q carries no certificate authority, so the proxy cannot verify %s.\n"+
		"  Point cluster_ca at the PEM file for this cluster, or set allow_missing_ca = true "+
		"if the API server uses a publicly trusted certificate", p.res.ContextName, p.host)
}

// caBytes returns the cluster authority, preferring the explicit file.
func (p *provider) caBytes() ([]byte, error) {
	if p.cfg.ClusterCA != "" {
		path := p.cfg.ClusterCA
		if !filepath.IsAbs(path) {
			path = filepath.Join(p.workdir, path)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read cluster_ca %s: %w", path, err)
		}
		return b, nil
	}
	return p.res.caBytes()
}

func (p *provider) Preflight(ctx context.Context, e *tool.Env) error {
	if err := p.resolve(e); err != nil {
		return err
	}
	if !p.cfg.inCluster() && p.cfg.ServiceAccount == "" {
		// The direct form carries the host credentials as they are. There is
		// no command to try.
		return nil
	}
	// Running the capture here turns a confusing runtime warning into a clear
	// message. nono runs the same command later, with the same stripped
	// environment, so a failure now is a failure then.
	kubeconfig := p.kubeconfig
	if p.cfg.inCluster() && p.mints() {
		// Build finally writes this file into the artifact directory, but
		// Preflight runs before nn writes anything, so the probe gets its own
		// copy.
		path, cleanup, err := writeTemp(p.hostKubeconfig)
		if err != nil {
			return err
		}
		defer cleanup()
		kubeconfig = path
	}
	argv := p.tokenCommand(kubeconfig)

	probe, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probe, argv[0], argv[1:]...)
	cmd.Env = captureEnv()
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		detail := err.Error()
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			detail = strings.TrimSpace(string(ee.Stderr))
		}
		if !p.mints() {
			return fmt.Errorf("cannot read the service account token with %s: %s",
				strings.Join(argv, " "), detail)
		}
		return fmt.Errorf("cannot mint a token for service account %q in namespace %q: %s",
			p.cfg.ServiceAccount, p.cfg.ServiceAccountNamespace, detail)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return fmt.Errorf("the token command %s returned nothing", strings.Join(argv, " "))
	}
	return nil
}

// writeTemp puts content in a private file and returns a function that removes
// it again.
func writeTemp(content []byte) (string, func(), error) {
	f, err := os.CreateTemp("", "nn-kubeconfig-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.Remove(f.Name()) }
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		cleanup()
		return "", nil, err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		cleanup()
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return f.Name(), cleanup, nil
}

// resolveKubectl returns an absolute path to a real kubectl binary.
//
// nono runs the capture command with a stripped environment. A version manager
// shim needs its own environment to work and fails there, which surfaces only
// as "credential capture command failed with exit code 2", so the shim is
// unwrapped here instead.
func resolveKubectl(name string) (string, error) {
	if filepath.IsAbs(name) {
		return name, nil
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("kubectl binary %q is not on PATH: %w", name, err)
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if !isShim(path) {
		return path, nil
	}
	if real, err := exec.Command("mise", "which", filepath.Base(name)).Output(); err == nil {
		if r := strings.TrimSpace(string(real)); r != "" {
			return r, nil
		}
	}
	return "", fmt.Errorf("%s is a version manager shim, which cannot run with the stripped "+
		"environment that nono gives a credential capture; set kubectl in "+
		"[tools.kubernetes] to a real binary path", path)
}

func isShim(path string) bool {
	return strings.Contains(path, "/shims/") || strings.Contains(path, "/.mise-bins/")
}

// captureEnv mirrors what nono hands a credential capture: the host
// environment without the proxy variables.
func captureEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		}
		out = append(out, kv)
	}
	return out
}

func (p *provider) Build(ctx context.Context, e *tool.Env) (*tool.Result, error) {
	if p.res == nil {
		if err := p.resolve(e); err != nil {
			return nil, err
		}
	}
	if !p.cfg.inCluster() && p.cfg.ServiceAccount == "" {
		return p.buildDirect(e)
	}
	return p.buildProxy(e)
}

// buildProxy is the default form. kubectl talks to the real API server, and
// nono's proxy intercepts the connection, adds the bearer token and verifies
// the cluster certificate on the far side. The token never enters the sandbox.
func (p *provider) buildProxy(e *tool.Env) (*tool.Result, error) {
	ca, err := p.caBytes()
	if err != nil {
		return nil, err
	}

	kubeDir := workspace.ProfileVar + "/kube"
	cfgBytes, err := proxyKubeconfig(p.name(), p.res.Cluster.Server, p.cfg.ServiceAccountNamespace, tokenEnv)
	if err != nil {
		return nil, err
	}

	artifacts := []tool.Artifact{
		{RelPath: "kube/config", Mode: 0o600, Content: cfgBytes},
	}
	// captureKubeconfig is what the token command reads on the host, which is
	// not the file the sandbox reads.
	captureKubeconfig := p.kubeconfig
	if len(p.hostKubeconfig) > 0 {
		captureKubeconfig = filepath.Join(e.ArtifactDir, "kube", "host.yaml")
		artifacts = append(artifacts, tool.Artifact{
			RelPath: "kube/host.yaml", Mode: 0o600, Content: p.hostKubeconfig,
		})
	}

	route := nono.CustomCredential{
		Upstream:      "https://" + p.host,
		CredentialKey: "cmd://" + routeName,
		EnvVar:        tokenEnv,
		InjectMode:    "header",
		InjectHeader:  "Authorization",
		CredentialFmt: "Bearer {}",
	}
	if len(ca) > 0 {
		// This is the cluster's own certificate authority. It belongs to the
		// leg between nono and the API server, which is the leg that still
		// proves the cluster's identity.
		//
		// tls_ca resolves $WORKDIR from the environment rather than expanding
		// it as a profile variable, which is why nn sets WORKDIR on the nono
		// process. Keeping the spelling relative is what makes the generated
		// profile the same on every machine.
		route.TLSCA = kubeDir + "/ca.pem"
		artifacts = append(artifacts, tool.Artifact{RelPath: "kube/ca.pem", Mode: 0o644, Content: ca})
	}

	f := &nono.Profile{
		CredentialCapture: map[string]nono.CredentialCapture{
			routeName: {
				Command:      p.tokenCommand(captureKubeconfig),
				TimeoutSecs:  30,
				CacheTTLSecs: p.cacheTTLSecs(),
			},
		},
		Network: &nono.Network{
			Credentials:       []string{routeName},
			CustomCredentials: map[string]nono.CustomCredential{routeName: route},
			AllowDomain:       []nono.Domain{{Domain: hostOnly(p.host)}},
		},
		// No filesystem grant: kubeDir sits inside the artifact directory,
		// which the base profile already grants recursively.
		Environment: &nono.Environment{
			// KUBERNETES_* is what a kubelet sets inside a pod; nothing here
			// produces it. The phantom token needs no entry either: nono
			// injects it after this filter runs, and both names below go
			// through set_vars, which nono applies after it too.
			SetVars: map[string]string{
				"KUBECONFIG": kubeDir + "/config",
				// kubectl writes a discovery cache. Left alone it uses
				// ~/.kube/cache, which the required deny_credentials group
				// blocks, and every command then fails.
				"KUBECACHEDIR": kubeDir + "/cache",
			},
		},
	}

	// On macOS the runner adds --trust-proxy-ca, because a Go client reads the
	// system trust store there. The profile deliberately states no
	// ca_lifecycle: an explicit "session" contradicts that flag and nono
	// refuses to start.
	return &tool.Result{Fragment: f, Artifacts: artifacts}, nil
}

// buildDirect is the fallback without a service account. It copies the host
// context's own credentials into the sandbox, which is weaker, and it does not
// work for a context whose credentials come from an exec plugin.
func (p *provider) buildDirect(e *tool.Env) (*tool.Result, error) {
	if _, isExec := p.res.User["exec"]; isExec {
		return nil, fmt.Errorf(
			"context %q authenticates with an exec plugin, which cannot run inside the sandbox; "+
				"set service_account to use the proxy form instead", p.res.ContextName)
	}
	ca, err := p.caBytes()
	if err != nil {
		return nil, err
	}
	kubeDir := workspace.ProfileVar + "/kube"
	var caPath string
	artifacts := []tool.Artifact{}
	if len(ca) > 0 {
		caPath = kubeDir + "/ca.pem"
		artifacts = append(artifacts, tool.Artifact{RelPath: "kube/ca.pem", Mode: 0o644, Content: ca})
	}
	cfgBytes, err := directKubeconfig(p.name(), p.res, caPath)
	if err != nil {
		return nil, err
	}
	artifacts = append(artifacts, tool.Artifact{RelPath: "kube/config", Mode: 0o600, Content: cfgBytes})

	f := &nono.Profile{
		Network: &nono.Network{AllowDomain: []nono.Domain{{Domain: hostOnly(p.host)}}},
		Environment: &nono.Environment{
			SetVars: map[string]string{
				"KUBECONFIG":   kubeDir + "/config",
				"KUBECACHEDIR": kubeDir + "/cache",
			},
		},
	}
	return &tool.Result{Fragment: f, Artifacts: artifacts}, nil
}

// tokenCommand is the argv nono runs on the host to mint a token. It runs
// outside the sandbox, with the user's own cluster credentials.
func (p *provider) tokenCommand(kubeconfig string) []string {
	if p.cfg.inCluster() && !p.mints() {
		// The kubelet rotates this file in place, so re-reading it is the
		// whole refresh mechanism. It needs no kubectl, which matters for an
		// agent image that carries none.
		return []string{"/bin/cat", p.pod.TokenFile}
	}
	bin := p.kubectl
	if bin == "" {
		bin = p.cfg.Kubectl
	}
	args := []string{bin}
	// The capture runs with a stripped environment, so a kubeconfig chosen in
	// the configuration has to be named on the command line. Without this the
	// command silently reads the default host kubeconfig instead.
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	if p.cfg.Context != "" {
		args = append(args, "--context", p.cfg.Context)
	}
	args = append(args, "create", "token", p.cfg.ServiceAccount,
		"-n", p.cfg.ServiceAccountNamespace, "--duration="+p.cfg.TokenTTL)
	return args
}

// cacheTTLSecs keeps the cached token comfortably inside its own lifetime, so
// a cached value never outlives the token it holds.
func (p *provider) cacheTTLSecs() int {
	if p.cfg.inCluster() && !p.mints() {
		// token_ttl describes a token that nn asks for. The kubelet's token
		// has a lifetime nn did not choose and cannot read, so the only safe
		// answer is to re-read the file often. Reading a file costs nothing.
		return 60
	}
	secs := int(p.ttl.Seconds() * 0.8)
	if secs < 60 {
		secs = 60
	}
	return secs
}

func (p *provider) name() string {
	if p.cfg.Context != "" {
		return p.cfg.Context
	}
	return p.res.ContextName
}

// hostOnly drops the port. api_server may name an IPv6 address, whose own
// colons make a plain cut wrong.
func hostOnly(hostPort string) string {
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		return h
	}
	return hostPort
}
