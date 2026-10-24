package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jrderuiter/nn/internal/tool"
)

// newCredentialCmd runs the credential handler of a tool. nono calls it from a
// credential_capture entry and reads the credential from stdout, so it is
// hidden and prints nothing else.
func newCredentialCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "credential <tool> [args...]",
		Short:              "Print a credential for a nono credential capture",
		Hidden:             true,
		DisableFlagParsing: true,
		Args:               cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, ok := tool.Credential(args[0])
			if !ok {
				return fmt.Errorf("no credential handler for tool %q; known handlers are %v", args[0], tool.Credentials())
			}
			if err := f(cmd.Context(), args[1:], cmd.OutOrStdout()); err != nil {
				return fmt.Errorf("credential %s: %w", args[0], err)
			}
			return nil
		},
	}
}
