package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/jderuiter/nn/internal/config"
	"github.com/jderuiter/nn/internal/discover"
	"github.com/jderuiter/nn/internal/nono"
	"github.com/jderuiter/nn/internal/runner"
)

// locate finds the config, honouring --config then NN_CONFIG then the search.
func locate(o *options) (discover.Dirs, error) {
	if o.configPath != "" {
		return discover.At(o.configPath)
	}
	if p := os.Getenv("NN_CONFIG"); p != "" {
		return discover.At(p)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return discover.Dirs{}, err
	}
	return discover.Find(cwd)
}

// prepare runs the whole pipeline up to, but not including, exec.
func prepare(mode config.Mode, o *options, child []string) (*nono.Plan, discover.Dirs, error) {
	dirs, err := locate(o)
	if err != nil {
		return nil, discover.Dirs{}, err
	}
	f, err := config.Load(dirs.ConfigPath)
	if err != nil {
		return nil, dirs, err
	}
	sec, dropped := config.Resolve(f, mode)
	o.applyCLI(sec)

	plan, err := nono.Build(sec, mode, dirs, dropped, nono.Options{
		Extra:       child,
		AppendExtra: o.appendArgs,
		NoWrappers:  o.noWrappers,
		Env:         o.envOverrides(),
	})
	if err != nil {
		return nil, dirs, fmt.Errorf("%s: %w", dirs.ConfigPath, err)
	}
	return plan, dirs, nil
}

func runExec(mode config.Mode, printOnly bool, o *options, child []string, stdout, stderr io.Writer) int {
	plan, _, err := prepare(mode, o, child)
	if err != nil {
		fmt.Fprintf(stderr, "nn: %v\n", err)
		return ExitUsage
	}

	if printOnly {
		return printPlan(plan, o.format, stdout, stderr)
	}

	// Dropped keys are reported so a mode switch never silently changes
	// behaviour without saying so.
	if len(plan.Dropped) > 0 && os.Getenv("NN_QUIET") == "" {
		fmt.Fprintf(stderr, "nn: note: %d key(s) not supported by `nono %s`, ignored: %v\n",
			len(plan.Dropped), plan.Mode, plan.Dropped)
	}

	err = runner.Exec(plan.Argv, plan.Env)
	// Exec only returns on failure.
	var nf *runner.NotFoundError
	if errors.As(err, &nf) {
		fmt.Fprintf(stderr, "nn: %v\n", describeNotFound(nf, plan))
		return ExitNotFound
	}
	fmt.Fprintf(stderr, "nn: exec %s: %v\n", plan.Argv[0], err)
	return ExitUsage
}

// describeNotFound says which link in the chain is missing, and how to fix it.
// A bare "executable file not found" would be the least useful possible
// message for a tool whose entire job is composing a four-binary chain.
func describeNotFound(nf *runner.NotFoundError, plan *nono.Plan) string {
	switch {
	case nf.Name == nono.DefaultBin:
		return fmt.Sprintf("%q not found on PATH\n"+
			"    install it: brew install nono   (https://nono.sh)\n"+
			"    or set `nono_bin:` in your config", nf.Name)
	case plan.Argv[0] == nf.Name:
		return fmt.Sprintf("wrapper binary %q not found on PATH\n"+
			"    it is the first entry in `wrappers:`\n"+
			"    install it, drop the wrapper, or run with --no-wrappers", nf.Name)
	default:
		return fmt.Sprintf("%q not found on PATH", nf.Name)
	}
}
