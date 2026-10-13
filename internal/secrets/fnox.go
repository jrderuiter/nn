// Package secrets resolves secret references through fnox.
//
// nn reads each value once, at launch, and hands it to nono in the environment
// of the nono process. The value never reaches a file or the sandbox
// environment: the proxy gives the child a phantom token instead.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type Resolver struct {
	Binary  string // fnox binary, "fnox" by default
	Config  string // -c path, optional
	Profile string // -P profile, optional
	// Imports replaces the search of fnox with these files, highest priority
	// first. nn sets it when a linked worktree lacks a file that the main
	// checkout has.
	Imports []string
}

func NewResolver(binary, config, profile string) *Resolver {
	if binary == "" {
		binary = "fnox"
	}
	return &Resolver{Binary: binary, Config: config, Profile: profile}
}

// command builds a call to fnox get. The cleanup function removes the
// temporary file that an import needs.
//
// fnox reads one -c file, so the imports go into a temporary file that
// imports them. That file holds paths only, never a value. fnox gives the
// first import priority over the ones after it.
func (r *Resolver) command(ctx context.Context, key string) (*exec.Cmd, func(), error) {
	// --non-interactive stops fnox from opening a browser or prompting behind
	// the agent's back.
	args := []string{"--non-interactive"}
	cleanup := func() {}
	config := r.Config
	if len(r.Imports) > 0 {
		f, err := os.CreateTemp("", "nn-fnox-*.toml")
		if err != nil {
			return nil, nil, fmt.Errorf("fnox import file: %w", err)
		}
		cleanup = func() { os.Remove(f.Name()) }
		var b strings.Builder
		b.WriteString("import = [")
		for i, p := range r.Imports {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(strconv.Quote(p))
		}
		b.WriteString("]\n")
		_, err = f.WriteString(b.String())
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("fnox import file: %w", err)
		}
		config = f.Name()
	}
	if config != "" {
		args = append(args, "-c", config)
	}
	if r.Profile != "" {
		args = append(args, "-P", r.Profile)
	}
	args = append(args, "get", key)
	return exec.CommandContext(ctx, r.Binary, args...), cleanup, nil
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
	cmd, cleanup, err := r.command(ctx, key)
	if err != nil {
		return "", err
	}
	defer cleanup()
	out, err := cmd.Output()
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
	cmd, cleanup, err := r.command(ctx, key)
	if err != nil {
		return err
	}
	defer cleanup()
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
