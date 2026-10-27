package kubernetes

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// execPlugin is the exec entry of a kubeconfig user.
type execPlugin struct {
	APIVersion         string   `yaml:"apiVersion"`
	Command            string   `yaml:"command"`
	Args               []string `yaml:"args"`
	Env                []envVar `yaml:"env"`
	InstallHint        string   `yaml:"installHint"`
	ProvideClusterInfo bool     `yaml:"provideClusterInfo"`
	InteractiveMode    string   `yaml:"interactiveMode"`
}

type envVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// execOf returns the exec plugin of a context, or nil when the user has none.
func execOf(r *resolved) (*execPlugin, error) {
	raw, ok := r.User["exec"]
	if !ok {
		return nil, nil
	}
	// The user is decoded as a plain map, so the entry is encoded again to
	// read it into its own type.
	b, err := yaml.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var p execPlugin
	if err := yaml.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("context %q has an unreadable exec entry: %w", r.ContextName, err)
	}
	if p.Command == "" {
		return nil, fmt.Errorf("the exec entry of context %q names no command", r.ContextName)
	}
	if p.InteractiveMode == "Always" {
		return nil, fmt.Errorf("the exec plugin of context %q needs a terminal (interactiveMode: Always), "+
			"and nono runs it without one", r.ContextName)
	}
	return &p, nil
}

// execToken runs the exec plugin of a context on the host and returns the
// token it prints. It reads the kubeconfig itself, because the credential
// handler has no other source.
func execToken(ctx context.Context, path, contextName string, env []string) (string, error) {
	kc, err := loadKubeconfig(path)
	if err != nil {
		return "", err
	}
	res, err := kc.resolveContext(contextName)
	if err != nil {
		return "", err
	}
	plugin, err := execOf(res)
	if err != nil {
		return "", err
	}
	if plugin == nil {
		return "", fmt.Errorf("context %q has no exec plugin", res.ContextName)
	}
	info, err := execInfo(plugin, res)
	if err != nil {
		return "", err
	}
	bin := plugin.Command
	// kubectl resolves a relative command against the kubeconfig, and a bare
	// name through PATH.
	if strings.ContainsRune(bin, filepath.Separator) && !filepath.IsAbs(bin) {
		bin = filepath.Join(filepath.Dir(path), bin)
	}
	cmd := exec.CommandContext(ctx, bin, plugin.Args...)
	cmd.Env = append(env, "KUBERNETES_EXEC_INFO="+string(info))
	for _, v := range plugin.Env {
		cmd.Env = append(cmd.Env, v.Name+"="+v.Value)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		if errors.Is(err, exec.ErrNotFound) && plugin.InstallHint != "" {
			detail += "\n" + strings.TrimSpace(plugin.InstallHint)
		}
		return "", fmt.Errorf("exec plugin %s of context %q failed: %s", plugin.Command, res.ContextName, detail)
	}
	var cred struct {
		Status struct {
			Token               string     `json:"token"`
			ExpirationTimestamp *time.Time `json:"expirationTimestamp"`
		} `json:"status"`
	}
	if err := json.Unmarshal(out, &cred); err != nil {
		return "", fmt.Errorf("exec plugin %s of context %q printed no ExecCredential: %w", plugin.Command, res.ContextName, err)
	}
	if cred.Status.Token == "" {
		return "", fmt.Errorf("exec plugin %s of context %q returned no token; "+
			"a client certificate cannot go through the proxy", plugin.Command, res.ContextName)
	}
	if t := cred.Status.ExpirationTimestamp; t != nil && !t.After(time.Now()) {
		return "", fmt.Errorf("exec plugin %s of context %q returned a token that expired at %s",
			plugin.Command, res.ContextName, t.Format(time.RFC3339))
	}
	return cred.Status.Token, nil
}

// execInfo is the KUBERNETES_EXEC_INFO that kubectl gives a plugin. nono runs
// the handler without a terminal, so the plugin must not ask anything.
func execInfo(p *execPlugin, r *resolved) ([]byte, error) {
	spec := map[string]any{"interactive": false}
	if p.ProvideClusterInfo {
		ca, err := r.caBytes()
		if err != nil {
			return nil, err
		}
		cl := map[string]any{"server": r.Cluster.Server}
		if len(ca) > 0 {
			cl["certificate-authority-data"] = base64.StdEncoding.EncodeToString(ca)
		}
		if r.Cluster.TLSServerName != "" {
			cl["tls-server-name"] = r.Cluster.TLSServerName
		}
		if r.Cluster.InsecureSkipTLSVerify {
			cl["insecure-skip-tls-verify"] = true
		}
		spec["cluster"] = cl
	}
	return json.Marshal(map[string]any{"apiVersion": p.APIVersion, "kind": "ExecCredential", "spec": spec})
}

// credential is the handler behind `nn credential kubernetes`. nono runs it
// on the host each time its cached token expires.
func credential(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("kubernetes", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var path, contextName string
	fs.StringVar(&path, "kubeconfig", "", "")
	fs.StringVar(&contextName, "context", "", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if path == "" || contextName == "" {
		return errors.New("--kubeconfig and --context are required")
	}
	token, err := execToken(ctx, path, contextName, os.Environ())
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(out, token)
	return err
}
