package kubernetes

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// kubeconfig models only the parts of a kubeconfig that nn reads or writes.
type kubeconfig struct {
	APIVersion     string         `yaml:"apiVersion"`
	Kind           string         `yaml:"kind"`
	CurrentContext string         `yaml:"current-context,omitempty"`
	Clusters       []namedCluster `yaml:"clusters"`
	Contexts       []namedContext `yaml:"contexts"`
	Users          []namedUser    `yaml:"users"`
	Preferences    map[string]any `yaml:"preferences,omitempty"`
}

type namedCluster struct {
	Name    string  `yaml:"name"`
	Cluster cluster `yaml:"cluster"`
}

type cluster struct {
	Server                   string `yaml:"server"`
	CertificateAuthority     string `yaml:"certificate-authority,omitempty"`
	CertificateAuthorityData string `yaml:"certificate-authority-data,omitempty"`
	InsecureSkipTLSVerify    bool   `yaml:"insecure-skip-tls-verify,omitempty"`
	TLSServerName            string `yaml:"tls-server-name,omitempty"`
}

type namedContext struct {
	Name    string     `yaml:"name"`
	Context ctxDetails `yaml:"context"`
}

type ctxDetails struct {
	Cluster   string `yaml:"cluster"`
	User      string `yaml:"user"`
	Namespace string `yaml:"namespace,omitempty"`
}

type namedUser struct {
	Name string         `yaml:"name"`
	User map[string]any `yaml:"user"`
}

// hostKubeconfigPath resolves the host kubeconfig, honouring KUBECONFIG.
func hostKubeconfigPath(home string, lookup func(string) (string, bool)) string {
	if v, ok := lookup("KUBECONFIG"); ok && v != "" {
		// KUBECONFIG may list several files. nn reads the first one, which is
		// the file kubectl itself treats as primary for writes.
		if i := indexByte(v, os.PathListSeparator); i >= 0 {
			return v[:i]
		}
		return v
	}
	return filepath.Join(home, ".kube", "config")
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func loadKubeconfig(path string) (*kubeconfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read kubeconfig %s: %w", path, err)
	}
	var kc kubeconfig
	if err := yaml.Unmarshal(raw, &kc); err != nil {
		return nil, fmt.Errorf("parse kubeconfig %s: %w", path, err)
	}
	return &kc, nil
}

// resolved is the part of a kubeconfig that one context points at.
type resolved struct {
	ContextName string
	Cluster     cluster
	User        map[string]any
	Namespace   string
}

// resolveContext finds a context by name, or the current context when name is
// empty, and returns the cluster and user it refers to.
func (kc *kubeconfig) resolveContext(name string) (*resolved, error) {
	if name == "" {
		name = kc.CurrentContext
	}
	if name == "" {
		return nil, fmt.Errorf("no context given and the kubeconfig has no current-context")
	}
	var ctx *ctxDetails
	for i := range kc.Contexts {
		if kc.Contexts[i].Name == name {
			ctx = &kc.Contexts[i].Context
			break
		}
	}
	if ctx == nil {
		return nil, fmt.Errorf("context %q not found in the kubeconfig (available: %v)", name, kc.contextNames())
	}
	var cl *cluster
	for i := range kc.Clusters {
		if kc.Clusters[i].Name == ctx.Cluster {
			cl = &kc.Clusters[i].Cluster
			break
		}
	}
	if cl == nil {
		return nil, fmt.Errorf("context %q refers to cluster %q, which the kubeconfig does not define", name, ctx.Cluster)
	}
	var usr map[string]any
	for i := range kc.Users {
		if kc.Users[i].Name == ctx.User {
			usr = kc.Users[i].User
			break
		}
	}
	return &resolved{ContextName: name, Cluster: *cl, User: usr, Namespace: ctx.Namespace}, nil
}

func (kc *kubeconfig) contextNames() []string {
	out := make([]string, 0, len(kc.Contexts))
	for _, c := range kc.Contexts {
		out = append(out, c.Name)
	}
	return out
}

// caBytes returns the cluster certificate authority, whether the kubeconfig
// stores it inline or refers to a file.
func (r *resolved) caBytes() ([]byte, error) {
	if r.Cluster.CertificateAuthorityData != "" {
		b, err := base64.StdEncoding.DecodeString(r.Cluster.CertificateAuthorityData)
		if err != nil {
			return nil, fmt.Errorf("decode certificate-authority-data: %w", err)
		}
		return b, nil
	}
	if r.Cluster.CertificateAuthority != "" {
		b, err := os.ReadFile(r.Cluster.CertificateAuthority)
		if err != nil {
			return nil, fmt.Errorf("read certificate-authority %s: %w", r.Cluster.CertificateAuthority, err)
		}
		return b, nil
	}
	return nil, nil
}

// proxyKubeconfig builds the kubeconfig that the sandbox uses in proxy mode.
//
// The server is the real API server. nono's proxy intercepts the connection,
// swaps the phantom token for the real one and checks the cluster certificate
// itself, so this file names no certificate authority and holds no real
// credential.
//
// The user must still present something. A kubeconfig with an empty user makes
// kubectl fall back to an interactive "Please enter Username:" prompt on the
// first 401. An exec plugin is the only form that can read a value that is
// known at launch rather than at generation, which is what the phantom token
// is.
//
// The cluster entry names no authority and skips nothing. kubectl trusts the
// interception certificate because nono makes its authority trusted: on macOS
// through the user trust store, which Go reads there, and elsewhere through the
// trust bundle variables it sets, which Go reads instead.
func proxyKubeconfig(name, server, namespace, tokenEnv string) ([]byte, error) {
	kc := kubeconfig{
		APIVersion:     "v1",
		Kind:           "Config",
		CurrentContext: name,
		Clusters:       []namedCluster{{Name: name, Cluster: cluster{Server: server}}},
		Contexts: []namedContext{{
			Name:    name,
			Context: ctxDetails{Cluster: name, User: name, Namespace: namespace},
		}},
		Users: []namedUser{{Name: name, User: map[string]any{
			"exec": map[string]any{
				"apiVersion": "client.authentication.k8s.io/v1",
				"command":    "/bin/sh",
				"args":       []string{"-c", execScript(tokenEnv)},
				// Never means kubectl fails with a message instead of asking a
				// question that nothing can answer inside a sandbox.
				"interactiveMode":    "Never",
				"provideClusterInfo": false,
			},
		}}},
	}
	return yaml.Marshal(kc)
}

// execScript prints an ExecCredential holding the phantom token that nono puts
// in the environment. The proxy redeems it for the real one.
func execScript(tokenEnv string) string {
	return `printf '{"apiVersion":"client.authentication.k8s.io/v1",` +
		`"kind":"ExecCredential","status":{"token":"%s"}}' ` +
		`"${` + tokenEnv + `:?nono did not set ` + tokenEnv + `}"`
}

// directKubeconfig builds a single-context kubeconfig that carries the host
// context's own credentials. It is the form for auth = "host", and it is weaker because the credential lands inside the sandbox.
func directKubeconfig(name string, r *resolved, caPath string) ([]byte, error) {
	cl := r.Cluster
	cl.CertificateAuthorityData = ""
	cl.CertificateAuthority = caPath
	cl = dialLoopback(cl)
	kc := kubeconfig{
		APIVersion:     "v1",
		Kind:           "Config",
		CurrentContext: name,
		Clusters:       []namedCluster{{Name: name, Cluster: cl}},
		Contexts: []namedContext{{
			Name:    name,
			Context: ctxDetails{Cluster: name, User: name, Namespace: r.Namespace},
		}},
		Users: []namedUser{{Name: name, User: r.User}},
	}
	return yaml.Marshal(kc)
}

// dialLoopback points a server on the unspecified address, which k3d writes
// as 0.0.0.0, at the loopback address. Go bypasses the proxy only for a
// loopback host, so kubectl sends 0.0.0.0 to the nono proxy, which refuses it.
// tls-server-name keeps the name that the host verifies the certificate
// against.
func dialLoopback(cl cluster) cluster {
	u, err := url.Parse(cl.Server)
	if err != nil {
		return cl
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsUnspecified() {
		return cl
	}
	loopback := "127.0.0.1"
	if ip.To4() == nil {
		loopback = "::1"
	}
	if cl.TLSServerName == "" {
		cl.TLSServerName = u.Hostname()
	}
	if port := u.Port(); port != "" {
		u.Host = net.JoinHostPort(loopback, port)
	} else if ip.To4() == nil {
		u.Host = "[" + loopback + "]"
	} else {
		u.Host = loopback
	}
	cl.Server = u.String()
	return cl
}
