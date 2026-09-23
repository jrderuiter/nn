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
			"inside nono. It is the same as nn -- <command>, spelled out for\n" +
			"scripts.\n\n" +
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
	addRunFlags(c.Flags())
	return c
}

func execPlan(ctx context.Context, p *plan) error {
	extra, err := p.resolveSecrets(ctx)
	if err != nil {
		return err
	}
	return nono.Exec(p.runArgs(), nono.Env(p.ws.Workdir, agentName(p.command), extra))
}
