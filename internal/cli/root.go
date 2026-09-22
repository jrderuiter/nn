// Package cli wires the command line.
package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// Version is set at build time with -ldflags.
var Version = "dev"

var opts options

// Execute runs the command line and returns the process exit code.
func Execute() int {
	root := newRoot()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "nn: "+err.Error())
		return 1
	}
	return 0
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "nn [flags] -- <command> [args...]",
		Short: "Run a command in a nono sandbox, built from declared capabilities",
		Long: strings.TrimSpace(`
nn builds a nono profile from the tools declared in nn.toml, writes the files
that profile refers to, and runs nono with it.

  nn -- claude          run claude with this project's tools
  nn init               generate the sandbox files without running anything
  nn doctor             check the configuration
  nn example            print a complete example nn.toml
`),
		Version:               Version,
		SilenceUsage:          true,
		SilenceErrors:         true,
		DisableFlagsInUseLine: true,
		Args:                  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCommand(cmd, args)
		},
	}

	pf := root.PersistentFlags()
	pf.StringVar(&opts.configPath, "config", "", "path to nn.toml, skipping the upward search")
	pf.StringArrayVar(&opts.only, "tool", nil, "use only this tool (repeatable)")
	pf.StringArrayVar(&opts.skip, "no-tool", nil, "skip this tool (repeatable)")
	pf.StringVar(&opts.workdir, "workdir", "", "working directory, defaulting to the current one")
	pf.BoolVar(&dryRun, "dry-run", false, "print the nono command instead of running it")
	pf.BoolVarP(&verbose, "verbose", "v", false, "print the nono command and the files written")
	pf.BoolVar(&showBanner, "banner", false, "show nono's capability table and status lines")
	pf.BoolVar(&showDiagnostics, "diagnostics", false, "show nono's report of the paths it blocked")

	// Cobra takes -v for --version unless the flag already exists, and -v is
	// more useful as verbose.
	root.Flags().BoolP("version", "V", false, "version for nn")

	root.AddCommand(newRunCmd(), newInitCmd(), newDoctorCmd(), newExampleCmd())
	return root
}

// splitAtDash separates nn's own arguments from the sandboxed command.
func splitAtDash(cmd *cobra.Command, args []string) (before, after []string) {
	d := cmd.ArgsLenAtDash()
	if d < 0 {
		return args, nil
	}
	return args[:d], args[d:]
}

// commandFor extracts the sandboxed command. Everything after -- belongs to the
// child, so cobra must not try to parse it.
func commandFor(cmd *cobra.Command, args []string) []string {
	if d := cmd.ArgsLenAtDash(); d >= 0 {
		return args[d:]
	}
	return args
}

func runCommand(cmd *cobra.Command, args []string) error {
	command := commandFor(cmd, args)
	if len(command) == 0 {
		return errNoCommand
	}
	if err := requireConfig(opts); err != nil {
		return err
	}
	p, err := build(context.Background(), opts, command)
	if err != nil {
		return err
	}
	if err := p.write(); err != nil {
		return err
	}
	return runExec(p)
}

func runExec(p *plan) error {
	args := p.runArgs()
	if verbose {
		p.trace(args)
	}
	if dryRun {
		// stdout, so the command can be piped straight into a shell.
		fmt.Println("nono " + strings.Join(quoteArgs(args), " "))
		return nil
	}
	return execPlan(context.Background(), p)
}
