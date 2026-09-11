package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jderuiter/nn/internal/discover"
)

const template = `# nn configuration — see https://nono.sh for nono itself.
#
# nn carries only the flags a nono profile CANNOT express. Everything else
# (filesystem grants, network policy, credentials, ports) belongs in
# profile.json. If a key here is rejected as unknown, nn will tell you where
# in the profile it lives instead.

# --- shared by every mode ---

profile: profile.json        # relative to this directory, or a profile name
command: [claude]            # use [] to let the profile's own binary: decide

allow_cwd: true              # the profile sets the access level; this flag
                             # skips the prompt, and only a flag can do that

# wrappers run before nono, left to right. Each entry is a command prefix and
# carries its own separator if it needs one — nn adds nothing.
# wrappers:
#   - [fnox, exec, --]

# env: sets variables on the OUTER chain — the wrappers and nono itself.
#
# Whether one reaches the SANDBOXED CHILD is your profile's decision:
#   * with no environment.allow_vars, nono inherits everything, so it does
#   * with a non-empty allow_vars, only matching names pass; others are
#     dropped at the boundary
#   * deny_vars strips matches even when allow_vars lists them
#   * NONO_* and PATH never reach the child — nono manages those. That is the
#     point: NONO_THEME below configures nono, and stops there.
#
# For variables meant purely for the child, prefer environment.set_vars in
# profile.json. nn does not duplicate it — and set_vars rejects PATH and
# NONO_* at load time, which is why those belong here and only here.
#
# Values expand ${VAR} / $VAR against YOUR SHELL environment. An unknown name
# is an error, not an empty string. Use $$ for a literal $. nono's own
# $WORKDIR / $NONO_CONFIG tokens work in profile.json, not here.
#
# env:
#   NONO_THEME: minimal
# env_unset:
#   - AWS_PROFILE

# --- per-mode ---
#
# A key here asserts it applies to that mode, so a mismatch is an error. The
# same key at the top level is simply ignored by modes that lack it, which is
# what lets one file serve nn and nn wrap without editing.
#
# The three modes differ: run has 69 flags, shell 52, wrap 36. wrap is direct
# mode with no supervisor, so it has no proxy, rollback or audit flags.

run:                         # nn      (also nn run)
  trust_proxy_ca: true       # trust the proxy CA so Go tooling works
  no_diagnostics: true
  # skip_dir: [node_modules]

# shell:                     # nn shell — takes no command; pick the shell:
#   shell_bin: /bin/zsh

# wrap:                      # nn wrap — for scripts and CI
#   command: [go, test, ./...]

# Escape hatch for anything nn has no key for; passed through verbatim.
# nono_args: []
`

func runInit(o *options, stdout, stderr io.Writer) int {
	target := o.configPath
	if target == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "nn: %v\n", err)
			return ExitUsage
		}
		target = filepath.Join(cwd, discover.DirName, discover.ConfigName)
	}

	if _, err := os.Stat(target); err == nil && !o.force {
		fmt.Fprintf(stderr, "nn: %s already exists\n    pass --force to overwrite it\n", target)
		return ExitUsage
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		fmt.Fprintf(stderr, "nn: %v\n", err)
		return ExitUsage
	}
	if err := os.WriteFile(target, []byte(template), 0o644); err != nil {
		fmt.Fprintf(stderr, "nn: %v\n", err)
		return ExitUsage
	}

	fmt.Fprintf(stdout, "wrote %s\n", target)
	profile := filepath.Join(filepath.Dir(target), "profile.json")
	if _, err := os.Stat(profile); err != nil {
		fmt.Fprintf(stdout, "\nthere is no profile.json next to it yet. Create one with:\n"+
			"  nono profile init <name>          # writes ~/.config/nono/profiles/<name>.json\n"+
			"then either copy it here, or set `profile: <name>` in the config.\n")
	}
	fmt.Fprintf(stdout, "\ncheck it with `nn doctor`, and see the invocation with `nn print`.\n")
	return ExitOK
}
