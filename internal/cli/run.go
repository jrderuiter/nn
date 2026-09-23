package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/jrderuiter/nn/internal/nono"
)

var (
	dryRun          bool
	verbose         bool
	showBanner      bool
	showDiagnostics bool
)

func newRunCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "run [flags] -- <command> [args...]",
		Short: "Run a command in the sandbox",
		Long: "run builds the nono profile from nn.toml, writes it and the files it\n" +
			"refers to into .nono/nn, resolves the secrets, and runs the command\n" +
			"inside nono.\n\n" +
			"  nn run -- claude\n" +
			"  nn run --dry-run -- claude",
		SilenceUsage:          true,
		DisableFlagsInUseLine: true,
		Args:                  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
		},
	}
	fs := c.Flags()
	fs.BoolVar(&dryRun, "dry-run", false, "print the nono command instead of running it")
	fs.BoolVarP(&verbose, "verbose", "v", false, "print the nono command and the files written")
	fs.BoolVar(&showBanner, "banner", false, "show nono's capability table and status lines")
	fs.BoolVar(&showDiagnostics, "diagnostics", false, "show nono's report of the paths it blocked")
	return c
}

func execPlan(ctx context.Context, p *plan) error {
	extra, err := p.resolveSecrets(ctx)
	if err != nil {
		return err
	}
	return nono.Exec(p.runArgs(), nono.Env(p.ws.Workdir, agentName(p.command), extra))
}
