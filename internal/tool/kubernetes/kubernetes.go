// Package kubernetes gives a sandboxed agent access to one or more Kubernetes
// clusters.
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
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

	"github.com/jrderuiter/nn/internal/nono"
	"github.com/jrderuiter/nn/internal/tool"
	"github.com/jrderuiter/nn/internal/workspace"
)

// Config is the [tools.kubernetes] table. Without clusters it describes one
// cluster. With clusters, auth, context, service_account, cluster_ca and
// allow_missing_ca move into each cluster, and the other settings here are
// the defaults that every cluster shares.
type Config struct {
	// Auth picks how the sandbox authenticates, and has no default.
	// "service-account" keeps every credential on the host. "host" copies the
	// credentials of the context into the sandbox, and exists for local test
	// clusters.
	Auth string `toml:"auth"`
	// Context names the kubeconfig context to use. Empty means the current one.
	Context string `toml:"context"`
	// ServiceAccount is the existing service account that nono mints a token
	// for on the host. nn never creates accounts or RBAC.
	ServiceAccount string `toml:"service_account"`
	// ServiceAccountNamespace is where the service account lives, defaulting to
	// "default". It is also the default namespace in the generated kubeconfig.
	ServiceAccountNamespace string `toml:"service_account_namespace"`
	// TokenTTL is how long a minted token stays valid, for example "1h".
	TokenTTL string `toml:"token_ttl"`
	// Kubectl is the binary used to mint tokens on the host.
	Kubectl string `toml:"kubectl"`
	// Kubeconfig overrides the host kubeconfig path.
	Kubeconfig string `toml:"kubeconfig"`
	// ClusterCA points at a PEM file holding the cluster's certificate
	// authority. Set it when the kubeconfig context does not carry one.
	ClusterCA string `toml:"cluster_ca"`
	// AllowMissingCA turns off nn's own check that a certificate authority
	// was found. nono has no option to skip upstream verification, so this
	// only helps when the API server uses a publicly trusted certificate.
	AllowMissingCA bool `toml:"allow_missing_ca"`
	// Current names the cluster that the sandbox kubeconfig selects. It is
	// required when there is more than one cluster.
	Current string `toml:"current"`
	// Clusters holds one table per cluster, keyed by the context name that
	// the sandbox kubeconfig gives it. It is set only from nn.toml.
	Clusters map[string]Cluster `toml:"clusters"`
}

// Cluster is one [tools.kubernetes.clusters.<name>] table. Its settings mean
// the same as in Config, and an empty one takes the value from Config.
type Cluster struct {
	Auth                    string `toml:"auth"`
	Context                 string `toml:"context"`
	ServiceAccount          string `toml:"service_account"`
	ServiceAccountNamespace string `toml:"service_account_namespace"`
	TokenTTL                string `toml:"token_ttl"`
	Kubectl                 string `toml:"kubectl"`
	Kubeconfig              string `toml:"kubeconfig"`
	ClusterCA               string `toml:"cluster_ca"`
	AllowMissingCA          bool   `toml:"allow_missing_ca"`
}

// The values of auth.
const (
	authServiceAccount = "service-account"
	authHost           = "host"
)

// routeName and tokenEnv are the names of the single cluster form. A named
// cluster adds its own suffix, so each one has its own route and token.
const routeName = "k8s"

// tokenEnv is the variable that carries the per-session phantom token into the
// sandbox. Nothing reads it: the proxy injects the real token. nono requires
// the field whenever credential_key uses the cmd:// scheme.
const tokenEnv = "K8S_TOKEN"

// clusterName limits a cluster key to what is safe in a route name, a
// variable name and a file name.
var clusterName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

type provider struct {
	targets []*target
	// current is the context the sandbox kubeconfig selects. Empty means the
	// only target, under its own name.
	current string
}

// target is one cluster and everything resolved for it.
type target struct {
	cfg Cluster
	// label is the context name in the sandbox kubeconfig. Empty means the
	// name of the host context, which is the single cluster form.
	label    string
	route    string
	tokenEnv string
	// caFile is the name of the certificate authority file, next to the
	// sandbox kubeconfig.
	caFile  string
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
	if cfg.ServiceAccountNamespace == "" {
		cfg.ServiceAccountNamespace = "default"
	}
	if len(cfg.Clusters) == 0 {
		if cfg.Current != "" {
			return nil, fmt.Errorf("current %q names a cluster, but there is no [tools.kubernetes.clusters] table", cfg.Current)
		}
		t, err := newTarget(Cluster{
			Auth: cfg.Auth, Context: cfg.Context, ServiceAccount: cfg.ServiceAccount,
			ServiceAccountNamespace: cfg.ServiceAccountNamespace, TokenTTL: cfg.TokenTTL,
			Kubectl: cfg.Kubectl, Kubeconfig: cfg.Kubeconfig, ClusterCA: cfg.ClusterCA,
			AllowMissingCA: cfg.AllowMissingCA,
		})
		if err != nil {
			return nil, err
		}
		t.route, t.tokenEnv, t.caFile = routeName, tokenEnv, "ca.pem"
		return &provider{targets: []*target{t}}, nil
	}
	return newClusters(cfg)
}

// newClusters builds the named cluster form. Each cluster says for itself
// what it is, so the keys that pick a cluster and its credentials are refused
// at the top, where they would silently apply to all of them.
func newClusters(cfg Config) (*provider, error) {
	for key, set := range map[string]bool{
		"auth": cfg.Auth != "", "context": cfg.Context != "", "service_account": cfg.ServiceAccount != "",
		"cluster_ca": cfg.ClusterCA != "", "allow_missing_ca": cfg.AllowMissingCA,
	} {
		if set {
			return nil, fmt.Errorf("%s belongs in each [tools.kubernetes.clusters.<name>] table "+
				"when clusters are declared", key)
		}
	}
	names := make([]string, 0, len(cfg.Clusters))
	for name := range cfg.Clusters {
		names = append(names, name)
	}
	sort.Strings(names)

	p := &provider{current: cfg.Current}
	idents := map[string]string{}
	for _, name := range names {
		if !clusterName.MatchString(name) {
			return nil, fmt.Errorf("cluster %q: a name holds only letters, digits, - and _", name)
		}
		ident := strings.ToLower(strings.ReplaceAll(name, "-", "_"))
		if other, dup := idents[ident]; dup {
			return nil, fmt.Errorf("clusters %q and %q would share the route %q; rename one of them",
				other, name, routeName+"_"+ident)
		}
		idents[ident] = name
		c := cfg.Clusters[name]
		if c.ServiceAccountNamespace == "" {
			c.ServiceAccountNamespace = cfg.ServiceAccountNamespace
		}
		if c.TokenTTL == "" {
			c.TokenTTL = cfg.TokenTTL
		}
		if c.Kubectl == "" {
			c.Kubectl = cfg.Kubectl
		}
		if c.Kubeconfig == "" {
			c.Kubeconfig = cfg.Kubeconfig
		}
		t, err := newTarget(c)
		if err != nil {
			return nil, fmt.Errorf("cluster %q: %w", name, err)
		}
		t.label = name
		t.route = routeName + "_" + ident
		t.tokenEnv = tokenEnv + "_" + strings.ToUpper(ident)
		t.caFile = "ca-" + name + ".pem"
		p.targets = append(p.targets, t)
	}
	switch {
	case p.current == "" && len(names) == 1:
		p.current = names[0]
	case p.current == "":
		return nil, fmt.Errorf("current is required with more than one cluster; set it to one of %v", names)
	case !slices.Contains(names, p.current):
		return nil, fmt.Errorf("current %q is not a cluster; set it to one of %v", p.current, names)
	}
	return p, nil
}

// newTarget checks the settings of one cluster.
func newTarget(cfg Cluster) (*target, error) {
	ttl, err := time.ParseDuration(cfg.TokenTTL)
	if err != nil {
		return nil, fmt.Errorf("token_ttl %q is not a duration: %w", cfg.TokenTTL, err)
	}
	if ttl < time.Minute {
		return nil, fmt.Errorf("token_ttl must be at least one minute, got %s", cfg.TokenTTL)
	}
	// auth has no default, so the choice between the two forms is always
	// written down. The weaker form puts the host credentials, often a cluster
	// admin, into the sandbox, and must never be what a missing key means.
	switch cfg.Auth {
	case "":
		return nil, fmt.Errorf("auth is required: set auth = %q to mint a token for service_account on the host, "+
			"or auth = %q to copy the context credentials into the sandbox", authServiceAccount, authHost)
	case authServiceAccount:
		if cfg.ServiceAccount == "" {
			return nil, fmt.Errorf("auth %q needs service_account; set auth = %q to copy the "+
				"context credentials into the sandbox instead", authServiceAccount, authHost)
		}
	case authHost:
		if cfg.ServiceAccount != "" {
			return nil, fmt.Errorf("auth %q uses the context credentials, so service_account %q would be ignored; "+
				"remove one of them", authHost, cfg.ServiceAccount)
		}
	default:
		return nil, fmt.Errorf("auth must be %q or %q, got %q", authServiceAccount, authHost, cfg.Auth)
	}
	return &target{cfg: cfg, ttl: ttl}, nil
}

func (p *provider) Name() string { return "kubernetes" }

// errorf names the cluster in the named form, and leaves the single form
// messages as they are.
func (t *target) errorf(err error) error {
	if t.label == "" || err == nil {
		return err
	}
	return fmt.Errorf("cluster %q: %w", t.label, err)
}

// resolveAll resolves every target, then makes sure that the proxy can tell
// them apart.
func (p *provider) resolveAll(e *tool.Env) error {
	for _, t := range p.targets {
		if err := t.errorf(t.resolve(e)); err != nil {
			return err
		}
	}
	// nono picks a route by upstream host alone. Two clusters behind one API
	// server would share the route, and one of them would get the other's
	// token, or a host credential would be replaced on the way.
	for i, a := range p.targets {
		for _, b := range p.targets[i+1:] {
			if a.host == b.host && (a.cfg.Auth == authServiceAccount || b.cfg.Auth == authServiceAccount) {
				return fmt.Errorf("clusters %q and %q both use the API server %s; "+
					"the proxy picks a token by host, so keep one of them", a.label, b.label, a.host)
			}
		}
	}
	return nil
}

// resolve works out everything Build needs. It reads files but changes
// nothing, so both Preflight and Build can call it.
func (p *target) resolve(e *tool.Env) error {
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

	if p.cfg.Auth == authHost {
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
func (p *target) checkCA() error {
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
func (p *target) caBytes() ([]byte, error) {
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
	if err := p.resolveAll(e); err != nil {
		return err
	}
	for _, t := range p.targets {
		if err := t.errorf(t.probe(ctx)); err != nil {
			return err
		}
	}
	return nil
}

// probe mints one token, as nono will at launch.
func (p *target) probe(ctx context.Context) error {
	if p.cfg.Auth == authHost {
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
	if p.targets[0].res == nil {
		if err := p.resolveAll(e); err != nil {
			return nil, err
		}
	}
	kubeDir := workspace.ProfileVar + "/kube"
	network := &nono.Network{}
	var captures map[string]nono.CredentialCapture
	var entries []kubeconfig
	var artifacts []tool.Artifact
	for _, t := range p.targets {
		var part *part
		var err error
		if t.cfg.Auth == authHost {
			part, err = t.buildDirect()
		} else {
			part, err = t.buildProxy(kubeDir)
		}
		if err != nil {
			return nil, t.errorf(err)
		}
		entries = append(entries, part.entry)
		artifacts = append(artifacts, part.artifacts...)
		if part.route != nil {
			if captures == nil {
				captures = map[string]nono.CredentialCapture{}
				network.CustomCredentials = map[string]nono.CustomCredential{}
			}
			captures[t.route] = part.capture
			network.Credentials = append(network.Credentials, t.route)
			network.CustomCredentials[t.route] = *part.route
		}
		if part.port != 0 && !slices.Contains(network.OpenPort, part.port) {
			network.OpenPort = append(network.OpenPort, part.port)
		}
		if part.domain != "" && !slices.ContainsFunc(network.AllowDomain, func(d nono.Domain) bool { return d.Domain == part.domain }) {
			network.AllowDomain = append(network.AllowDomain, nono.Domain{Domain: part.domain})
		}
	}
	current := p.current
	if current == "" {
		current = p.targets[0].name()
	}
	cfgBytes, err := yaml.Marshal(combine(current, entries))
	if err != nil {
		return nil, err
	}
	artifacts = append(artifacts, tool.Artifact{RelPath: "kube/config", Mode: 0o600, Content: cfgBytes})

	f := &nono.Profile{
		CredentialCapture: captures,
		Network:           network,
		// No filesystem grant: kubeDir sits inside the artifact directory,
		// which the base profile already grants recursively.
		Environment: &nono.Environment{
			// KUBERNETES_* is what a kubelet sets inside a pod; nothing here
			// produces it. The phantom tokens need no entry either: nono
			// injects them after this filter runs, and both names below go
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

// part is what one cluster adds to the profile and to the sandbox kubeconfig.
type part struct {
	entry     kubeconfig
	artifacts []tool.Artifact
	// route and capture are set for the service-account form only.
	route   *nono.CustomCredential
	capture nono.CredentialCapture
	// domain is the host to allow, and port the local port to open instead.
	domain string
	port   int
}

// buildProxy is the service-account form. kubectl talks to the real API
// server, and nono's proxy intercepts the connection, adds the bearer token
// and verifies the cluster certificate on the far side. The token never
// enters the sandbox.
func (p *target) buildProxy(kubeDir string) (*part, error) {
	ca, err := p.caBytes()
	if err != nil {
		return nil, err
	}
	route := nono.CustomCredential{
		Upstream:      "https://" + p.host,
		CredentialKey: "cmd://" + p.route,
		EnvVar:        p.tokenEnv,
		InjectMode:    "header",
		InjectHeader:  "Authorization",
		CredentialFmt: "Bearer {}",
	}
	out := &part{
		entry: proxyEntry(p.name(), p.res.Cluster.Server, p.cfg.ServiceAccountNamespace, p.tokenEnv),
		route: &route,
		capture: nono.CredentialCapture{
			Command:      p.tokenCommand(),
			TimeoutSecs:  30,
			CacheTTLSecs: p.cacheTTLSecs(),
		},
		domain: hostOnly(p.host),
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
		route.TLSCA = kubeDir + "/" + p.caFile
		out.artifacts = append(out.artifacts, tool.Artifact{RelPath: "kube/" + p.caFile, Mode: 0o644, Content: ca})
	}
	return out, nil
}

// buildDirect is the host form. It copies the host context's own credentials
// into the sandbox, which is weaker, and it does not work for a context whose
// credentials come from an exec plugin.
func (p *target) buildDirect() (*part, error) {
	if _, isExec := p.res.User["exec"]; isExec {
		return nil, fmt.Errorf(
			"context %q authenticates with an exec plugin, which cannot run inside the sandbox; "+
				"set auth = %q and service_account to use the proxy form instead", p.res.ContextName, authServiceAccount)
	}
	ca, err := p.caBytes()
	if err != nil {
		return nil, err
	}
	out := &part{}
	var caPath string
	if len(ca) > 0 {
		// kubectl reads this path, not nono, so it cannot use the $WORKDIR
		// spelling of the profile. kubectl resolves a relative path against
		// the directory of the kubeconfig, which is where the file lands.
		caPath = p.caFile
		out.artifacts = append(out.artifacts, tool.Artifact{RelPath: "kube/" + p.caFile, Mode: 0o644, Content: ca})
	}
	out.entry = directEntry(p.name(), p.res, caPath)
	if port, ok := loopbackPort(p.host); ok {
		// Go never sends a loopback address through a proxy, so kubectl
		// connects straight to the port, and the proxy's domain list does
		// not apply. macOS offers no connect-only grant, so open_port also
		// lets the sandbox listen on the port, which the cluster holds.
		out.port = port
	} else {
		out.domain = hostOnly(p.host)
	}
	return out, nil
}

// tokenCommand is the argv nono runs on the host to mint a token. It runs
// outside the sandbox, with the user's own cluster credentials.
func (p *target) tokenCommand() []string {
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
func (p *target) cacheTTLSecs() int {
	secs := int(p.ttl.Seconds() * 0.8)
	if secs < 60 {
		secs = 60
	}
	return secs
}

func (p *target) name() string {
	if p.label != "" {
		return p.label
	}
	if p.cfg.Context != "" {
		return p.cfg.Context
	}
	return p.res.ContextName
}

// loopbackPort returns the port of an API server on this machine, as a local
// test cluster such as k3d runs it. A server without a port uses 443.
func loopbackPort(hostPort string) (int, bool) {
	host, portText, err := net.SplitHostPort(hostPort)
	if err != nil {
		host, portText = hostPort, "443"
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !(ip.IsLoopback() || ip.IsUnspecified())) {
		return 0, false
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return 0, false
	}
	return port, true
}

func hostOnly(hostPort string) string {
	h, _, found := strings.Cut(hostPort, ":")
	if !found {
		return hostPort
	}
	return h
}
