package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newInitCmd(opts *options) *cobra.Command {
	c := &cobra.Command{
		Use:   "init",
		Short: "Generate the sandbox files without running anything",
		Long: "init writes the generated profile and everything it refers to into\n" +
			".nono/nn, and stops there. It is what run does before it hands over to\n" +
			"nono, so the files can be read, kept, or used with nono directly.\n\n" +
			"When the project has no nn.toml, init writes the example one first, and\n" +
			"builds from that. An existing file is used as it is. Every other\n" +
			"command needs that file, so init is how a project starts.\n\n" +
			"init writes the shared profile. Pass --agent to write the profile of\n" +
			"one agent instead.",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			o := *opts
			o.stderr = cmd.ErrOrStderr()
			if err := ensureConfig(o); err != nil {
				return err
			}
			p, err := build(cmd.Context(), o, nil)
			if err != nil {
				return err
			}
			if err := p.write(); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, p.ws.ProfilePath(p.agent))
			for _, a := range p.artifacts {
				fmt.Fprintln(out, p.ws.Path(a.RelPath))
			}
			return nil
		},
	}
	return c
}
