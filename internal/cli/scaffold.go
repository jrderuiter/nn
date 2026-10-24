package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jrderuiter/nn/internal/config"
)

// errNoConfig stops a run in a project that has no nn.toml. Running with
// defaults only would leave egress unrestricted, and nothing on disk would say
// what the sandbox was.
var errNoConfig = errors.New("no " + config.FileName + " found; run `nn init` to create one")

// projectDir is the directory that the upward search for nn.toml starts from.
func projectDir(o options) (string, error) {
	if o.workdir != "" {
		return o.workdir, nil
	}
	return os.Getwd()
}

// requireConfig fails when the upward search finds no nn.toml. An explicit
// --config is left to the loader, which reports a missing file by its path.
func requireConfig(o options) error {
	if o.configPath != "" {
		return nil
	}
	dir, err := projectDir(o)
	if err != nil {
		return err
	}
	if config.Find(dir) == "" {
		return errNoConfig
	}
	return nil
}

// ensureConfig writes the example nn.toml when the project has none, so that
// init is the one step that marks a directory as an nn project, and the file it
// leaves names every setting there is to change. The git identity of the user
// fills in [tools.git], and the sections in o.enable are written switched on.
//
// It looks for the project file the same way the loader does, by walking up,
// and writes nothing when that search finds one. Writing a second file in a
// subdirectory would shadow the project file above it, which is never what the
// user means. An explicit --config is left alone too: a path the user named and
// misspelled is an error, not an invitation to write a new file.
func ensureConfig(ctx context.Context, o options) error {
	enable, err := sectionSet(o.enable)
	if err != nil {
		return err
	}
	if o.configPath != "" {
		if len(enable) > 0 {
			return errors.New("--enable writes a new " + config.FileName + ", so it cannot be used with --config")
		}
		return nil
	}
	dir, err := projectDir(o)
	if err != nil {
		return err
	}
	if found := config.Find(dir); found != "" {
		if len(enable) > 0 {
			return fmt.Errorf("--enable writes a new %s, but %s exists", config.FileName, found)
		}
		return nil
	}
	identity := o.gitIdentity
	if identity == nil {
		identity = gitIdentity
	}
	name, email := identity(ctx, dir)
	path := filepath.Join(dir, config.FileName)
	body := example(exampleValues{name: name, email: email, enable: enable})
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	o.warnf("wrote %s; edit it to suit the project", path)
	return nil
}

// sectionSet checks the names given to --enable against the sections of the
// example.
func sectionSet(names []string) (map[string]bool, error) {
	known := exampleSections()
	out := map[string]bool{}
	for _, n := range names {
		if !slices.Contains(known, n) {
			return nil, fmt.Errorf("unknown section %q; known sections are %s", n, strings.Join(known, ", "))
		}
		out[n] = true
	}
	return out, nil
}

// gitIdentity reads user.name and user.email as git resolves them in dir, so
// an includeIf for the directory applies. A value that git does not have is
// empty, and the example then keeps its placeholder.
func gitIdentity(ctx context.Context, dir string) (name, email string) {
	get := func(key string) string {
		cmd := exec.CommandContext(ctx, "git", "config", "--get", key)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	return get("user.name"), get("user.email")
}
