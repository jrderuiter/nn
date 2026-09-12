// Package nono assembles the command line that runs a config's sandbox.
package nono

import (
	"fmt"
	"strconv"

	"github.com/jderuiter/nn/internal/config"
	"github.com/jderuiter/nn/internal/discover"
	"github.com/jderuiter/nn/internal/herdr"
)

// DefaultBin is the nono executable, overridable with nono_bin.
const DefaultBin = "nono"

// Options are the CLI-supplied inputs to a build.
type Options struct {
	Extra       []string // tokens after --
	AppendExtra bool     // --append: add to command instead of replacing it
	NoWrappers  bool     // --no-wrappers
	Env         EnvOverrides
	Environ     []string // defaults to os.Environ() when nil
}

// Plan is a fully resolved invocation, ready to exec or print.
type Plan struct {
	Argv []string // argv[0] is the program to exec
	Env  []string // complete environment for execve

	// For `nn print` and doctor.
	EnvApplied map[string]string // overrides, not the inherited environment
	EnvRemoved []string
	Dropped    []string // top-level keys the mode does not accept
	Mode       config.Mode
}

// Build turns a resolved section into an invocation.
func Build(sec *config.Section, mode config.Mode, dirs discover.Dirs, dropped []string, opts Options) (*Plan, error) {
	environ := opts.Environ
	if environ == nil {
		environ = osEnviron()
	}

	command, err := resolveCommand(sec, mode, opts)
	if err != nil {
		return nil, err
	}
	// With an empty command the profile names the program, so look there for
	// the herdr hint rather than losing it.
	agentHint := command
	if len(agentHint) == 0 && sec.Profile.Present {
		p := discover.ResolveProfile(sec.Profile.Value, dirs.ConfigDir)
		if bin, ok := discover.ProfileBinary(p); ok {
			agentHint = []string{bin}
		}
	}

	var argv []string

	// 1. Wrappers, verbatim and in order. nn never synthesises a separator
	// for them: [fnox, exec, --] carries its own, and `direnv exec .` has
	// none by design.
	if !opts.NoWrappers {
		for _, w := range sec.Wrappers {
			argv = append(argv, w...)
		}
	}

	// 2. nono, then the mode.
	bin := DefaultBin
	if sec.NonoBin.Present && sec.NonoBin.Value != "" {
		bin = sec.NonoBin.Value
	}
	argv = append(argv, bin, mode.String())

	// 3. Typed flags in table order — fixed at compile time, so output is
	// byte-stable and two prints differ only where the config did.
	for _, spec := range config.Specs {
		if spec.Modes&mode == 0 {
			continue
		}
		argv = append(argv, emit(&spec, sec, dirs)...)
	}

	// 4. The raw escape hatch, after typed flags so it can override them
	// (clap is last-wins for most flags).
	argv = append(argv, sec.NonoArgs...)

	// 5. The child command. `nono shell` takes none; it picks the shell with
	// --shell. Everywhere else, exactly one --, even when the first token is
	// not flag-like: it costs nothing and removes `nn -- --version` as a
	// class of problem.
	if mode != config.ModeShell && len(command) > 0 {
		argv = append(argv, "--")
		argv = append(argv, command...)
	}

	env, applied, removed, err := buildEnvWithHerdr(sec, agentHint, opts.Env, environ)
	if err != nil {
		return nil, err
	}

	return &Plan{
		Argv: argv, Env: env,
		EnvApplied: applied, EnvRemoved: removed,
		Dropped: dropped, Mode: mode,
	}, nil
}

func buildEnvWithHerdr(sec *config.Section, command []string, cli EnvOverrides, environ []string) ([]string, map[string]string, []string, error) {
	if sec.Herdr.Is(false) {
		return BuildEnv(sec, cli, environ)
	}
	inferred := map[string]string{}
	herdr.Apply(inferred, command, config.EnvironMap(environ))
	id, ok := inferred[herdr.EnvVar]
	if !ok {
		return BuildEnv(sec, cli, environ)
	}
	if _, explicit := sec.Env[herdr.EnvVar]; explicit {
		return BuildEnv(sec, cli, environ) // an explicit env: entry wins
	}

	// Copy rather than mutate: the caller's section is not ours to change,
	// and the inferred value is only a default.
	withHint := *sec
	withHint.Env = make(map[string]string, len(sec.Env)+1)
	for k, v := range sec.Env {
		withHint.Env[k] = v
	}
	withHint.Env[herdr.EnvVar] = id
	return BuildEnv(&withHint, cli, environ)
}

func resolveCommand(sec *config.Section, mode config.Mode, opts Options) ([]string, error) {
	if mode == config.ModeShell {
		if len(opts.Extra) > 0 {
			return nil, fmt.Errorf("`nono shell` takes no program\n" +
				"    drop the trailing arguments, or use `nn run -- ...`")
		}
		return nil, nil
	}

	// An explicit `command: []` hands the choice to the profile's own
	// `binary` key. That is distinguishable from an absent command, because
	// YAML gives a non-nil empty slice, and nono warns when both are set.
	if sec.Command != nil && len(sec.Command) == 0 && len(opts.Extra) == 0 {
		return nil, nil
	}

	command := sec.Command
	if len(opts.Extra) > 0 {
		if opts.AppendExtra {
			command = append(append([]string{}, command...), opts.Extra...)
		} else {
			command = opts.Extra
		}
	}
	if len(command) == 0 {
		return nil, fmt.Errorf("no command to run\n" +
			"    set `command:` in the config, or pass one: nn -- <program>")
	}
	return command, nil
}

// emit renders one flag. An unset value emits nothing; so does an explicit
// false for a switch, which exists so a CLI --allow-cwd=false can beat a
// config `allow_cwd: true`.
func emit(spec *config.FlagSpec, sec *config.Section, dirs discover.Dirs) []string {
	v := spec.Get(sec)
	switch spec.Kind {
	case config.KindSwitch:
		if b, ok := v.(config.Bool); ok && b.Is(true) {
			return []string{spec.Flag}
		}
	case config.KindString:
		if s, ok := v.(config.Str); ok && s.Present {
			return []string{spec.Flag, anchorValue(spec, s.Value, dirs)}
		}
	case config.KindInt:
		if i, ok := v.(config.Int); ok && i.Present {
			return []string{spec.Flag, strconv.Itoa(i.Value)}
		}
	case config.KindCount:
		if c, ok := v.(config.Count); ok && c.Present {
			// Repeat the flag rather than folding to -vvv: separate tokens
			// keep `nn print` output paste-able.
			out := make([]string, 0, c.Value)
			for range c.Value {
				out = append(out, spec.Flag)
			}
			return out
		}
	case config.KindStringSlice:
		ss, ok := v.([]string)
		if !ok {
			return nil
		}
		out := make([]string, 0, len(ss)*2)
		for _, item := range ss {
			out = append(out, spec.Flag, anchorValue(spec, item, dirs))
		}
		return out
	}
	return nil
}

// anchorValue makes a path-shaped value absolute. nono runs from the user's
// CWD, which may be far below the project root, so a relative path in the
// config would otherwise resolve against the wrong directory.
func anchorValue(spec *config.FlagSpec, v string, dirs discover.Dirs) string {
	switch spec.Anchor {
	case config.AnchorConfig:
		// profile: may name a built-in or registry profile rather than a file.
		if spec.Key == "profile" {
			return discover.ResolveProfile(v, dirs.ConfigDir)
		}
		return discover.ResolvePath(v, dirs.ConfigDir)
	case config.AnchorRoot:
		return discover.ResolvePath(v, dirs.Root)
	}
	return v
}
