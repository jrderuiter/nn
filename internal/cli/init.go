package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "init [-- <command>]",
		Short: "Generate the sandbox files without running anything",
		Long: "init writes the generated profile and everything it refers to into\n" +
			".nono/nn, and stops there. It is what run does before it hands over to\n" +
			"nono, so the files can be read, kept, or used with nono directly.\n\n" +
			"Pass a command after -- when the agent changes the base profile.",
		SilenceUsage: true,
		Args:         cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := build(context.Background(), opts, commandFor(cmd, args))
			if err != nil {
				return err
			}
			if err := p.write(); err != nil {
				return err
			}
			fmt.Println(p.ws.ProfilePath())
			for _, a := range p.artifacts {
				fmt.Println(p.ws.Path(a.RelPath))
			}
			return nil
		},
	}
	return c
}
