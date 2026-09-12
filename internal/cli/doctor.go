package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/jderuiter/nn/internal/config"
	"github.com/jderuiter/nn/internal/discover"
	"github.com/jderuiter/nn/internal/nono"
	"github.com/jderuiter/nn/internal/runner"
)

type report struct {
	w              io.Writer
	warns, errored int
}

func (r *report) ok(format string, a ...any) {
	fmt.Fprintf(r.w, "  ok    %s\n", fmt.Sprintf(format, a...))
}

func (r *report) warn(format string, a ...any) {
	r.warns++
	fmt.Fprintf(r.w, "  warn  %s\n", fmt.Sprintf(format, a...))
}

func (r *report) fail(format string, a ...any) {
	r.errored++
	fmt.Fprintf(r.w, "  FAIL  %s\n", fmt.Sprintf(format, a...))
}

// runDoctor checks the things that otherwise fail from the wrong layer: a
// missing binary three processes down, or a profile nono rejects at startup.
func runDoctor(o *options, stdout, stderr io.Writer) int {
	r := &report{w: stdout}

	dirs, err := locate(o)
	if err != nil {
		fmt.Fprintf(stderr, "nn: %v\n", err)
		return ExitUsage
	}
	r.ok("config      %s", dirs.ConfigPath)

	f, err := config.Load(dirs.ConfigPath)
	if err != nil {
		fmt.Fprintf(stderr, "nn: %v\n", err)
		return ExitUsage
	}
	r.ok("config parses and validates")

	// Every mode, not just the default: a typo in a block you rarely invoke
	// should surface here rather than in CI.
	for _, m := range []config.Mode{config.ModeRun, config.ModeShell, config.ModeWrap} {
		sec, dropped := config.Resolve(f, m)
		o.applyCLI(sec)
		plan, err := nono.Build(sec, m, dirs, dropped, nono.Options{Env: o.envOverrides()})
		if err != nil {
			if m == config.ModeRun {
				r.fail("nn %-5s  %v", m, firstLine(err))
			} else {
				r.warn("nn %-5s  %v", m, firstLine(err))
			}
			continue
		}
		if len(dropped) > 0 {
			r.ok("nn %-5s  ignores top-level %s", m, strings.Join(dropped, ", "))
		} else {
			r.ok("nn %-5s  builds", m)
		}
		if m == config.ModeRun {
			checkBinaries(r, sec, plan)
			checkProfile(r, sec, dirs)
			checkEnvShadowing(r, sec)
		}
	}

	switch {
	case r.errored > 0:
		fmt.Fprintf(stdout, "\n%d error(s), %d warning(s)\n", r.errored, r.warns)
		return ExitUsage
	case r.warns > 0:
		fmt.Fprintf(stdout, "\n%d warning(s)\n", r.warns)
		return 1
	}
	fmt.Fprintf(stdout, "\nall good\n")
	return ExitOK
}

func checkBinaries(r *report, sec *config.Section, plan *nono.Plan) {
	pathVar := runner.PathOf(plan.Env)
	if p, ok := lookupEnv(plan.EnvApplied, "PATH"); ok {
		r.ok("PATH        overridden by env.PATH (%s)", truncate(p, 48))
	}

	for i, w := range sec.Wrappers {
		if len(w) == 0 {
			continue
		}
		if _, err := runner.LookPathIn(w[0], pathVar); err != nil {
			r.fail("wrapper     %q not on PATH (wrappers[%d])", w[0], i)
		} else {
			r.ok("wrapper     %s", strings.Join(w, " "))
		}
		// direnv exec . legitimately has no separator, so this is advisory.
		if w[len(w)-1] != "--" {
			r.warn("wrapper     wrappers[%d] does not end in \"--\"; nn adds no separator", i)
		}
	}

	bin := nono.DefaultBin
	if sec.NonoBin.Present && sec.NonoBin.Value != "" {
		bin = sec.NonoBin.Value
	}
	resolved, err := runner.LookPathIn(bin, pathVar)
	if err != nil {
		r.fail("nono        %q not on PATH — brew install nono (https://nono.sh)", bin)
		return
	}
	version := "?"
	if out, err := exec.Command(resolved, "--version").Output(); err == nil {
		version = strings.TrimSpace(string(out))
	}
	r.ok("nono        %s (%s)", resolved, version)

	// The child may legitimately exist only inside the sandbox, so warn only.
	if len(sec.Command) > 0 {
		if _, err := runner.LookPathIn(sec.Command[0], pathVar); err != nil {
			r.warn("command     %q not on the host PATH (fine if it exists only in the sandbox)", sec.Command[0])
		} else {
			r.ok("command     %s", strings.Join(sec.Command, " "))
		}
	}
}

func checkProfile(r *report, sec *config.Section, dirs discover.Dirs) {
	if !sec.Profile.Present {
		return
	}
	raw := sec.Profile.Value
	if !discover.IsPathLike(raw, dirs.ConfigDir) {
		r.ok("profile     %s (name, resolved by nono)", raw)
		return
	}
	resolved := discover.ResolveProfile(raw, dirs.ConfigDir)
	if _, err := os.Stat(resolved); err != nil {
		r.fail("profile     %s does not exist", resolved)
		return
	}
	out, err := exec.Command("nono", "profile", "validate", resolved).CombinedOutput()
	if err != nil {
		r.fail("profile     %s rejected by nono:\n        %s", resolved, indent(strings.TrimSpace(string(out))))
		return
	}
	r.ok("profile     %s", resolved)
}

// A typed key beats its NONO_* env equivalent, because clap only falls back to
// env when the flag is absent. So such an env entry is dead config.
func checkEnvShadowing(r *report, sec *config.Section) {
	for _, spec := range config.Specs {
		envName := "NONO_" + strings.ToUpper(strings.ReplaceAll(spec.Key, "-", "_"))
		if _, ok := sec.Env[envName]; ok && config.IsKeySet(sec, spec.Key) {
			r.warn("env         %s is shadowed by %s (the flag wins)", envName, spec.Key)
		}
	}
}

func lookupEnv(m map[string]string, k string) (string, bool) { v, ok := m[k]; return v, ok }

func firstLine(err error) string {
	return strings.SplitN(err.Error(), "\n", 2)[0]
}

func indent(s string) string { return strings.ReplaceAll(s, "\n", "\n        ") }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
