package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jrderuiter/nn/internal/config"
	"github.com/jrderuiter/nn/internal/tool"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the configuration and everything it depends on",
		Long: "doctor reads the configuration, tests every tool, and makes sure that the\n" +
			"profile those tools produce is valid. It writes nothing and runs nothing.",
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

	wd := opts.workdir
	if wd == "" {
		var err error
		if wd, err = os.Getwd(); err != nil {
			return err
		}
	}
	cfg, err := config.Load(config.Options{Dir: wd, Explicit: opts.configPath, Keys: envKeys()})
	if err != nil {
		return err
	}

	fmt.Println("\nconfiguration")
	if len(cfg.Sources()) == 0 {
		fmt.Println("  none found; nn will run with defaults only")
	}
	for _, s := range cfg.Sources() {
		fmt.Printf("  %s\n", s)
	}

	fmt.Println("\ntools")
	// A section without an entry in enabled_tools is the one mistake that a changed
	// configuration model makes likely, and it fails quietly: the tool is
	// simply absent from the sandbox.
	for _, name := range idle(cfg) {
		fmt.Printf("  %-12s configured, but not in enabled_tools\n", name)
	}
	if len(cfg.EnabledTools) == 0 {
		fmt.Printf("  none enabled; available: %s\n", strings.Join(tool.Known(), ", "))
		return nil
	}

	pr, err := prepare(opts)
	if err != nil {
		return err
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

	p, err := build(ctx, opts, nil)
	if err != nil {
		return err
	}
	// The profile is checked in a scratch copy, so doctor leaves the project
	// untouched.
	if err := p.validateOnly(); err != nil {
		return err
	}
	fmt.Println("\nprofile   valid")
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

// idle lists the tools that have a section but are not enabled, sorted.
func idle(cfg *config.Config) []string {
	var out []string
	for name := range cfg.Tools {
		if !slices.Contains(cfg.EnabledTools, name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
