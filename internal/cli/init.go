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
			"The new file takes the committer identity from git. Pass --enable to\n" +
			"switch sections on in it, for example:\n\n" +
			"  nn init --enable agents.claude,tools.github,tools.mise\n\n" +
			"init writes the shared profile. Pass --agent to write the profile of\n" +
			"one agent instead.",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			o := *opts
			o.stderr = cmd.ErrOrStderr()
			if err := ensureConfig(cmd.Context(), o); err != nil {
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
	c.Flags().StringSliceVar(&opts.enable, "enable", nil,
		"switch these `sections` on in a new nn.toml, such as agents.claude or tools.github")
	_ = c.RegisterFlagCompletionFunc("enable", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return exampleSections(), cobra.ShellCompDirectiveNoFileComp
	})
	return c
}
