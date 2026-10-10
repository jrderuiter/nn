// Package cli wires the command line.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

// Execute runs the command line and returns the process exit code.
//
// An interrupt cancels the context rather than killing nn outright, so a fnox
// or kubectl call that waits for a touch or a password stops with it, and nn
// reports the cancel as an error.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := newRoot(&options{})
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "nn: "+err.Error())
		return 1
	}
	return 0
}

// newRoot builds the command tree around opts. The flags write into opts, and
// every command reads from it, so a test can hand in a fixture such as
// gitRemotes before it runs a command.
func newRoot(opts *options) *cobra.Command {
	root := &cobra.Command{
		Use:   "nn",
		Short: "Run a command in a nono sandbox, built from declared tools",
		Long: strings.TrimSpace(`
nn builds a nono profile from the tools declared in nn.toml, writes the files
that profile refers to, and runs nono with it.

  nn run -- claude      run claude with this project's tools
  nn run --agent claude -- bash
                        run bash in the sandbox that claude gets
  nn init               generate the sandbox files without running anything
  nn profile            print the generated profile
  nn doctor             check the configuration
  nn example            print a complete example nn.toml
`),
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate(versionString())

	pf := root.PersistentFlags()
	pf.StringVar(&opts.configPath, "config", "", "path to nn.toml, skipping the upward search")
	pf.StringVar(&opts.workdir, "workdir", "", "working directory, defaulting to the current one")
	pf.StringVar(&opts.agent, "agent", "", "apply the [agents.<name>] section with this `name`, whatever the command")

	// Cobra takes -v for --version unless the flag already exists. -V keeps
	// the version apart from -v, which is verbose on nn run.
	root.Flags().BoolP("version", "V", false, "version for nn")

	root.AddCommand(newRunCmd(opts), newInitCmd(opts), newProfileCmd(opts), newDoctorCmd(opts), newExampleCmd())
	return root
}

// commandFor extracts the sandboxed command. Everything after -- belongs to the
// child, so cobra must not try to parse it.
func commandFor(cmd *cobra.Command, args []string) []string {
	if d := cmd.ArgsLenAtDash(); d >= 0 {
		return args[d:]
	}
	return args
}

func runExec(ctx context.Context, p *plan, stdout io.Writer) error {
	args := p.runArgs()
	if p.opts.verbose {
		p.trace(args)
	}
	if p.opts.dryRun {
		// stdout, so the command can be piped straight into a shell.
		fmt.Fprintln(stdout, "nono "+strings.Join(quoteArgs(args), " "))
		return nil
	}
	return execPlan(ctx, p)
}
