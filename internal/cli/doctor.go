package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jrderuiter/nn/internal/tool"
	"github.com/jrderuiter/nn/internal/workspace"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the configuration and everything it depends on",
		Long: "doctor reads the configuration, tests every tool, and makes sure that the\n" +
			"profiles those tools produce are valid: the shared profile, and one per\n" +
			"[agents.<name>] section. Pass --agent to check one agent only. It writes\n" +
			"nothing and runs nothing.",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return doctor(context.Background())
		},
	}
}

func doctor(ctx context.Context) error {
	fmt.Println(versionString())

	fmt.Println("tools on the host")
	for _, b := range []string{"nono", "fnox", "kubectl", "git"} {
		path, err := exec.LookPath(b)
		if err != nil {
			fmt.Printf("  %-8s not found on PATH\n", b)
			continue
		}
		fmt.Printf("  %-8s %s\n", b, path)
	}

	if err := requireConfig(opts); err != nil {
		return err
	}
	pr, err := prepare(opts)
	if err != nil {
		return err
	}
	cfg := pr.cfg

	fmt.Println("\nconfiguration")
	for _, s := range cfg.Sources() {
		fmt.Printf("  %s\n", s)
	}

	fmt.Println("\ntools")
	if len(cfg.Tools) == 0 {
		fmt.Printf("  none configured; available: %s\n", strings.Join(tool.Known(), ", "))
		return nil
	}

	// Each tool is checked on its own, so one broken setting does not hide the
	// state of the others.
	failed := 0
	for _, p := range pr.providers {
		if err := p.Preflight(ctx, pr.env); err != nil {
			failed++
			fmt.Printf("  %-12s %s\n", p.Name(), firstLine(err))
			for _, line := range restLines(err) {
				fmt.Printf("  %-12s %s\n", "", line)
			}
			continue
		}
		fmt.Printf("  %-12s ok\n", p.Name())
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d tools cannot work as configured", failed, len(pr.providers))
	}

	// Every agent section gives a profile of its own, and a broken pack in
	// one of them must not wait until that agent first runs. --agent narrows
	// the check to one.
	agents := append([]string{""}, agentNames(cfg)...)
	if opts.agent != "" {
		agents = []string{opts.agent}
	}
	fmt.Println("\nprofiles")
	invalid := 0
	for _, agent := range agents {
		o := opts
		o.agent = agent
		label := workspace.ProfileFile(agent)
		p, err := build(ctx, o, nil)
		if err == nil {
			// The profile is checked in a scratch copy, so doctor leaves the
			// project untouched.
			err = p.validateOnly()
		}
		if err != nil {
			invalid++
			fmt.Printf("  %-22s %s\n", label, err)
			continue
		}
		fmt.Printf("  %-22s valid\n", label)
	}
	if invalid > 0 {
		return fmt.Errorf("%d of %d profiles are not valid", invalid, len(agents))
	}
	return nil
}

// firstLine and restLines keep a multi line tool error aligned under its name.
func firstLine(err error) string {
	lines := strings.Split(strings.TrimSpace(err.Error()), "\n")
	return lines[0]
}

func restLines(err error) []string {
	lines := strings.Split(strings.TrimSpace(err.Error()), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines[1:] {
		out = append(out, strings.TrimSpace(l))
	}
	return out
}
