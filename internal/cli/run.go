package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/jrderuiter/nn/internal/nono"
)

func newRunCmd(opts *options) *cobra.Command {
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
			o := *opts
			o.stderr = cmd.ErrOrStderr()
			if err := requireConfig(o); err != nil {
				return err
			}
			// Before the build, so secrets resolve in the process that
			// launches nono, and only once.
			if !o.dryRun {
				if err := nono.ExecWithAgent(agentName(command)); err != nil {
					return err
				}
			}
			p, err := build(cmd.Context(), o, command)
			if err != nil {
				return err
			}
			if err := p.write(); err != nil {
				return err
			}
			return runExec(cmd.Context(), p, cmd.OutOrStdout())
		},
	}
	fs := c.Flags()
	fs.BoolVar(&opts.dryRun, "dry-run", false, "print the nono command instead of running it")
	fs.BoolVarP(&opts.verbose, "verbose", "v", false, "print the nono command and the files written")
	fs.BoolVar(&opts.banner, "banner", false, "show nono's capability table and status lines")
	fs.BoolVar(&opts.diagnostics, "diagnostics", false, "show nono's report of the paths it blocked")
	return c
}

func execPlan(ctx context.Context, p *plan) error {
	extra, err := p.resolveSecrets(ctx)
	if err != nil {
		return err
	}
	return nono.Exec(p.runArgs(), nono.Env(p.ws.Workdir, agentName(p.command), extra))
}
