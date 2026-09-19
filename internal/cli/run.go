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
	return c
}

func execPlan(p *plan) error {
	return nono.Exec(p.runArgs(), nono.Env(p.ws.Workdir, agentName(p.command)))
}
