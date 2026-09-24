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
}

const routeName = "k8s"

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
}

func init() {
	// The prototype describes the shape, not the defaults: it is only read for
	// the field names the environment can set.
	tool.Register("kubernetes", New, func() any { return &Config{} })
}

func New(md toml.MetaData, prim toml.Primitive) (tool.Provider, error) {
	cfg := Config{TokenTTL: "1h", Kubectl: "kubectl", ServiceAccountNamespace: "default"}
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
	if cfg.ServiceAccountNamespace == "" {
		cfg.ServiceAccountNamespace = "default"
	}
	return &provider{cfg: cfg, ttl: ttl}, nil
}

func (p *provider) Name() string { return "kubernetes" }

// resolve works out everything Build needs. It reads files but changes
// nothing, so both Preflight and Build can call it.
func (p *provider) resolve(e *tool.Env) error {
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
	if p.cfg.ServiceAccount == "" {
		return nil
	}
	// Minting a token here turns a confusing runtime warning into a clear
	// message. nono runs the same command later, with the same stripped
	// environment, so a failure now is a failure then.
	probe, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probe, p.tokenCommand()[0], p.tokenCommand()[1:]...)
	cmd.Env = captureEnv()
	if out, err := cmd.Output(); err != nil {
		var ee *exec.ExitError
		detail := err.Error()
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			detail = strings.TrimSpace(string(ee.Stderr))
		}
		return fmt.Errorf("cannot mint a token for service account %q in namespace %q: %s",
			p.cfg.ServiceAccount, p.cfg.ServiceAccountNamespace, detail)
	} else if len(strings.TrimSpace(string(out))) == 0 {
		return fmt.Errorf("kubectl create token returned nothing for service account %q", p.cfg.ServiceAccount)
	}
	return nil
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
	if p.cfg.ServiceAccount == "" {
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

	route := nono.CustomCredential{
		Upstream:      "https://" + p.host,
		CredentialKey: "cmd://" + routeName,
		EnvVar:        tokenEnv,
		InjectMode:    "header",
		InjectHeader:  "Authorization",
		CredentialFmt: "Bearer {}",
	}
	artifacts := []tool.Artifact{
		{RelPath: "kube/config", Mode: 0o600, Content: cfgBytes},
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
				Command:      p.tokenCommand(),
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
func (p *provider) tokenCommand() []string {
	bin := p.kubectl
	if bin == "" {
		bin = p.cfg.Kubectl
	}
	args := []string{bin}
	// The capture runs with a stripped environment, so a kubeconfig chosen in
	// the configuration has to be named on the command line. Without this the
	// command silently reads the default host kubeconfig instead.
	if p.kubeconfig != "" {
		args = append(args, "--kubeconfig", p.kubeconfig)
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

func hostOnly(hostPort string) string {
	h, _, found := strings.Cut(hostPort, ":")
	if !found {
		return hostPort
	}
	return h
}
