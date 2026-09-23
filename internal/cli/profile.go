package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/jrderuiter/nn/internal/nono"
)

// newProfileCmd prints the merged profile.
//
// It prints rather than writes, like example, so the profile can be read or
// piped without touching .nono/nn. The artifacts that the profile refers to are
// not written, so hand the output to nono only after nn init.
func newProfileCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "profile [--tool <name>]... [--as-mixin]",
		Short: "Print the generated profile",
		Long: "profile builds the nono profile and prints it to stdout. It writes\n" +
			"nothing and resolves no secret.\n\n" +
			"Pass --tool once for each tool to include only those tools. Every name\n" +
			"must be a tool that nn.toml enables. Without --tool, the profile holds\n" +
			"every configured tool.\n\n" +
			"--as-mixin leaves out the base layer and the [nono] settings, so the\n" +
			"output holds only what the tools add, for another profile to extend.\n" +
			"The base layer is what nn gives every run: the working directory grant\n" +
			"and the basic environment variables, such as PATH and HOME.\n\n" +
			"  nn profile\n" +
			"  nn profile --tool kubernetes --as-mixin > kubernetes.json",
		SilenceUsage:          true,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireConfig(opts); err != nil {
				return err
			}
			p, err := build(context.Background(), opts, nil)
			if err != nil {
				return err
			}
			body, err := nono.Marshal(p.profile)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(body)
			return err
		},
	}
	c.Flags().StringArrayVar(&opts.only, "tool", nil, "include only the tool with this `name` (repeatable)")
	c.Flags().BoolVar(&opts.mixin, "as-mixin", false, "leave out the base layer and the [nono] settings")
	return c
}
