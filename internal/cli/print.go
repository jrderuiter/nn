package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/jderuiter/nn/internal/nono"
)

// safeArg matches values that need no quoting in a shell.
var safeArg = regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,-]+$`)

// Quote renders s so a shell reads it back as one word.
func Quote(s string) string {
	if s != "" && safeArg.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func printPlan(p *nono.Plan, format string, stdout, stderr io.Writer) int {
	if len(p.Dropped) > 0 {
		fmt.Fprintf(stderr, "nn: note: not supported by `nono %s`, ignored: %s\n",
			p.Mode, strings.Join(p.Dropped, ", "))
	}

	switch format {
	case "", "shell":
		fmt.Fprintln(stdout, shellForm(p))
	case "lines":
		for _, k := range sortedKeys(p.EnvApplied) {
			fmt.Fprintf(stdout, "%s=%s\n", k, p.EnvApplied[k])
		}
		for _, k := range p.EnvRemoved {
			fmt.Fprintf(stdout, "-%s\n", k)
		}
		fmt.Fprintln(stdout)
		for _, a := range p.Argv {
			fmt.Fprintln(stdout, a)
		}
	case "json":
		out := struct {
			Env     map[string]string `json:"env"`
			Unset   []string          `json:"unset"`
			Argv    []string          `json:"argv"`
			Dropped []string          `json:"dropped"`
			Mode    string            `json:"mode"`
		}{p.EnvApplied, p.EnvRemoved, p.Argv, p.Dropped, p.Mode.String()}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(stderr, "nn: %v\n", err)
			return ExitUsage
		}
	default:
		fmt.Fprintf(stderr, "nn: unknown --format %q (want shell, lines or json)\n", format)
		return ExitUsage
	}
	return ExitOK
}

// shellForm renders the invocation as one paste-able command.
//
// Only the overrides are shown, never the inherited environment. Removals need
// `env -u`, since assignment-prefix syntax cannot express them — but the
// no-removals case is both the common one and materially cleaner, so it keeps
// the bare prefix form.
func shellForm(p *nono.Plan) string {
	var b strings.Builder
	if len(p.EnvRemoved) > 0 {
		b.WriteString("env")
		for _, k := range p.EnvRemoved {
			b.WriteString(" -u " + Quote(k))
		}
		b.WriteString(" \\\n")
	}
	for _, k := range sortedKeys(p.EnvApplied) {
		b.WriteString(k + "=" + Quote(p.EnvApplied[k]) + " \\\n")
	}
	for i, a := range p.Argv {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(Quote(a))
	}
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
