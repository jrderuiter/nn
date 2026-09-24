package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
// leaves names every setting there is to change.
//
// It looks for the project file the same way the loader does, by walking up,
// and writes nothing when that search finds one. Writing a second file in a
// subdirectory would shadow the project file above it, which is never what the
// user means. An explicit --config is left alone too: a path the user named and
// misspelled is an error, not an invitation to write a new file.
func ensureConfig(o options) error {
	if o.configPath != "" {
		return nil
	}
	dir, err := projectDir(o)
	if err != nil {
		return err
	}
	if config.Find(dir) != "" {
		return nil
	}
	path := filepath.Join(dir, config.FileName)
	if err := os.WriteFile(path, []byte(example()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	o.warnf("wrote %s; edit it to suit the project", path)
	return nil
}
