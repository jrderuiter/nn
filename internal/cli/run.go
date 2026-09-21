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
		Use:                   "run [flags] -- <command> [args...]",
		Short:                 "Run a command in the sandbox",
		Long:                  "run is the explicit form of the root command, for use in scripts.",
		SilenceUsage:          true,
		DisableFlagsInUseLine: true,
		Args:                  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCommand(cmd, args)
		},
	}
	return c
}

func execPlan(ctx context.Context, p *plan) error {
	extra, err := p.resolveSecrets(ctx)
	if err != nil {
		return err
	}
	return nono.Exec(p.runArgs(), nono.Env(p.ws.Workdir, agentName(p.command), extra))
}
