package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jrderuiter/nn/internal/config"
)

// ensureConfig writes a minimal nn.toml when the project has none, so that a
// first run in a new project starts from a file the user can edit rather than
// from defaults that are nowhere written down.
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
	dir := o.workdir
	if dir == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			return err
		}
	}
	if config.Find(dir) != "" {
		return nil
	}
	path := filepath.Join(dir, config.FileName)
	if err := os.WriteFile(path, []byte(minimalConfig()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Fprintf(os.Stderr, "nn: wrote %s; edit it, or run `nn example` for every setting\n", path)
	return nil
}

// minimalConfig is the file a new project starts from. It is short on purpose:
// `nn example` is the complete one, and a first file that has to be read before
// it can be changed is a file nobody changes.
func minimalConfig() string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	p("# nn configuration. Run `nn example` for a file with every section and")
	p("# every setting, commented.")
	p("")
	p("[nono]")
	p("# nono profiles to extend, merged before the generated parts. An agent")
	p("# pack such as nolabs-ai/claude goes here.")
	p("extends = [\"default\"]")
	p("")
	p("# One of nono's built in network allowlists: minimal, developer,")
	p("# claude-code, codex, opencode, enterprise. minimal grants the LLM APIs")
	p("# and nothing else. An empty value leaves egress unrestricted.")
	p("network_profile = \"minimal\"")
	p("")
	p("# Writing a [tools.<name>] section is what turns that tool on. These are")
	p("# the common ones. Run `nn example` for the rest, and for their settings.")
	p("# [tools.git]")
	p("# name = \"Your Name\"")
	p("# email = \"you@example.com\"")
	p("")
	p("# [tools.github]")
	p("# secret = \"GITHUB_TOKEN\"")

	return b.String()
}
