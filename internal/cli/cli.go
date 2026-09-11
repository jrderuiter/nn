// Package cli is nn's command-line front end.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jderuiter/nn/internal/config"
	"github.com/jderuiter/nn/internal/nono"
)

// Exit codes. 2 rather than 1 for usage errors, so nn's own failures never
// collide with a child's exit 1; 127 follows shell convention for "not found".
const (
	ExitOK       = 0
	ExitUsage    = 2
	ExitNotFound = 127
)

// Split separates nn's own arguments from the child command at the first --.
//
// This runs before any flag parsing, which is what keeps nn from ever
// swallowing a flag meant for the child: `nn -- go test -v` hands six opaque
// tokens through, and flag.Parse never sees -v.
func Split(args []string) (own, child []string, hasSep bool) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:], true
		}
	}
	return args, nil, false
}

type options struct {
	configPath string
	format     string
	print      bool
	noWrappers bool
	appendArgs bool
	force      bool

	profile      config.Str
	allowCwd     config.Bool
	trustProxyCA config.Bool
	dryRun       config.Bool
	envSet       map[string]string
	envUnset     []string
}

// Main runs nn. args excludes the program name.
func Main(args []string, stdout, stderr io.Writer) int {
	own, child, _ := Split(args)

	cmd := "run"
	if len(own) > 0 && !strings.HasPrefix(own[0], "-") {
		cmd, own = own[0], own[1:]
	}

	mode := config.ModeRun
	printOnly := false
	switch cmd {
	case "run", "shell", "wrap":
		mode, _ = config.ParseMode(cmd)
	case "print":
		printOnly = true
	case "init", "doctor", "version", "help":
		// handled below
	default:
		fmt.Fprintf(stderr, "nn: unknown command %q\n    commands: run, shell, wrap, print, init, doctor, version\n", cmd)
		return ExitUsage
	}

	opts := &options{envSet: map[string]string{}}
	fs := flag.NewFlagSet("nn", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { usage(stderr) }
	registerFlags(fs, opts)

	if err := fs.Parse(own); err != nil {
		return ExitUsage
	}
	if opts.print {
		printOnly = true
	}

	switch cmd {
	case "version":
		return printVersion(stdout)
	case "help":
		usage(stdout)
		return ExitOK
	case "init":
		return runInit(opts, stdout, stderr)
	case "doctor":
		return runDoctor(opts, stdout, stderr)
	}

	return runExec(mode, printOnly, opts, child, stdout, stderr)
}

func registerFlags(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.configPath, "config", "", "path to nn.yml (skips the search)")
	fs.StringVar(&o.configPath, "c", "", "shorthand for --config")
	fs.BoolVar(&o.print, "print", false, "print the resolved invocation instead of running it")
	fs.StringVar(&o.format, "format", "shell", "print format: shell, lines or json")
	fs.BoolVar(&o.noWrappers, "no-wrappers", false, "skip the wrappers chain")
	fs.BoolVar(&o.appendArgs, "append", false, "append -- args to command instead of replacing it")
	fs.BoolVar(&o.force, "force", false, "overwrite an existing file (nn init)")

	fs.Var(&o.profile, "profile", "override the profile")
	fs.Var(&o.allowCwd, "allow-cwd", "pass --allow-cwd to nono")
	fs.Var(config.Neg{B: &o.allowCwd}, "no-allow-cwd", "suppress --allow-cwd")
	fs.Var(&o.trustProxyCA, "trust-proxy-ca", "pass --trust-proxy-ca to nono")
	fs.Var(config.Neg{B: &o.trustProxyCA}, "no-trust-proxy-ca", "suppress --trust-proxy-ca")
	fs.Var(&o.dryRun, "dry-run", "pass --dry-run to nono (see `nn print` to inspect nn itself)")

	fs.Var(envSetFlag{o}, "env", "set an environment variable, KEY=VALUE (repeatable)")
	fs.Var(envSetFlag{o}, "e", "shorthand for --env")
	fs.Var(envUnsetFlag{o}, "unset", "remove an inherited environment variable (repeatable)")
	fs.Var(envUnsetFlag{o}, "u", "shorthand for --unset")
}

type envSetFlag struct{ o *options }

func (f envSetFlag) String() string { return "" }
func (f envSetFlag) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	if !ok {
		return fmt.Errorf("expected KEY=VALUE, got %q", s)
	}
	if err := config.ValidEnvKey(k); err != nil {
		return fmt.Errorf("%q: %v", k, err)
	}
	f.o.envSet[k] = v
	return nil
}

type envUnsetFlag struct{ o *options }

func (f envUnsetFlag) String() string { return "" }
func (f envUnsetFlag) Set(s string) error {
	if err := config.ValidEnvKey(s); err != nil {
		return fmt.Errorf("%q: %v", s, err)
	}
	f.o.envUnset = append(f.o.envUnset, s)
	return nil
}

func (o *options) envOverrides() nono.EnvOverrides {
	return nono.EnvOverrides{Set: o.envSet, Unset: o.envUnset}
}

// applyCLI overlays the handful of flags that have a config equivalent. An
// unset flag must not clobber the config, which is what the tri-state buys.
func (o *options) applyCLI(sec *config.Section) {
	sec.Profile = sec.Profile.Or(o.profile)
	sec.AllowCwd = sec.AllowCwd.Or(o.allowCwd)
	sec.TrustProxyCA = sec.TrustProxyCA.Or(o.trustProxyCA)
	sec.DryRun = sec.DryRun.Or(o.dryRun)
}

func usage(w io.Writer) {
	fmt.Fprint(w, `nn — run a command under the nono sandbox, configured by .nono/nn.yml

USAGE
  nn [flags] [-- command...]          same as `+"`nn run`"+`
  nn run|shell|wrap [flags] [-- ...]  pick the nono execution mode
  nn print [flags]                    show the invocation without running it
  nn init [--force]                   write a starter .nono/nn.yml
  nn doctor                           check the config and the binaries it needs
  nn version

FLAGS
  -c, --config <path>   config file to use, skipping the directory search
      --profile <p>     override the profile
      --print           print the resolved invocation instead of running it
      --format <f>      print format: shell (default), lines, json
      --append          append -- args to command: instead of replacing it
      --no-wrappers     skip the wrappers chain
  -e, --env KEY=VALUE   set an environment variable (repeatable)
  -u, --unset KEY       remove an inherited environment variable (repeatable)
      --allow-cwd       pass --allow-cwd to nono (--no-allow-cwd to suppress)
      --trust-proxy-ca  pass --trust-proxy-ca (--no-trust-proxy-ca to suppress)
      --dry-run         pass --dry-run to nono; to inspect nn itself use `+"`nn print`"+`

NOTES
  -c is nn's own config file. nono's -c capability manifest is reachable
  through the `+"`config:`"+` key in nn.yml.

  Everything after -- goes to the child command untouched.

  Set NN_DEBUG=1 for nn's own diagnostics on stderr.
`)
}

var _ = os.Exit
