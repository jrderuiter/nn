// Package secrets resolves secret references through fnox.
//
// nn never reads a secret value itself. It emits a nono credential_capture
// entry that runs fnox on the host when the proxy needs the credential, so the
// value stays out of nn's memory and out of the sandbox environment.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type Resolver struct {
	Binary  string // fnox binary, "fnox" by default
	Config  string // -c path, optional
	Profile string // -P profile, optional
}

func NewResolver(binary, config, profile string) *Resolver {
	if binary == "" {
		binary = "fnox"
	}
	return &Resolver{Binary: binary, Config: config, Profile: profile}
}

// baseArgs are the flags that every call shares. --non-interactive stops fnox
// from opening a browser or prompting behind the agent's back.
func (r *Resolver) baseArgs() []string {
	args := []string{"--non-interactive"}
	if r.Config != "" {
		args = append(args, "-c", r.Config)
	}
	if r.Profile != "" {
		args = append(args, "-P", r.Profile)
	}
	return args
}

// CaptureCommand is the argv that nono runs on the host to fetch a secret.
func (r *Resolver) CaptureCommand(key string) []string {
	return append(append([]string{r.Binary}, r.baseArgs()...), "get", key)
}

// Available reports whether the fnox binary is on PATH.
func (r *Resolver) Available() error {
	if _, err := exec.LookPath(r.Binary); err != nil {
		return fmt.Errorf("fnox binary %q not found on PATH: %w", r.Binary, err)
	}
	return nil
}

// Get resolves a key to its value. The caller keeps it only long enough to
// hand it to nono, and never writes it to a file.
func (r *Resolver) Get(ctx context.Context, key string) (string, error) {
	if err := r.Available(); err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, r.Binary, append(r.baseArgs(), "get", key)...).Output()
	if err != nil {
		var ee *exec.ExitError
		detail := err.Error()
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			detail = strings.TrimSpace(string(ee.Stderr))
		}
		return "", fmt.Errorf("fnox cannot resolve secret %q: %s", key, detail)
	}
	value := strings.TrimRight(string(out), "\n")
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("fnox resolved secret %q to an empty value", key)
	}
	return value, nil
}

// Check makes sure that a key resolves, without printing or returning the
// value. It exists so `nn doctor` and the preflight fail early with a clear
// message instead of the agent seeing a 401 much later.
func (r *Resolver) Check(ctx context.Context, key string) error {
	if err := r.Available(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, r.Binary, append(r.baseArgs(), "get", key)...)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		detail := ""
		if errors.As(err, &ee) {
			detail = strings.TrimSpace(string(ee.Stderr))
		}
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("fnox cannot resolve secret %q: %s", key, detail)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return fmt.Errorf("fnox resolved secret %q to an empty value", key)
	}
	return nil
}
