// Package agent maps a known command to its nono base profile.
package agent

import "path/filepath"

// Agent describes a command that nono already has a profile for.
type Agent struct {
	Name string
	// Extends is the nono profile that the generated profile extends.
	Extends string
	// AllowVars are the environment variables the agent itself needs, on top
	// of the minimal base list.
	AllowVars []string
}

var known = []Agent{
	{
		Name:      "claude",
		Extends:   "nolabs-ai/claude",
		AllowVars: []string{"ANTHROPIC_*", "CLAUDE_*"},
	},
	{
		Name:      "codex",
		Extends:   "codex",
		AllowVars: []string{"OPENAI_*", "CODEX_*"},
	},
}

// Lookup finds the agent for a command. It matches on the base name, so an
// absolute path such as /Users/me/.local/bin/claude still resolves.
func Lookup(command string) (Agent, bool) {
	base := filepath.Base(command)
	for _, a := range known {
		if a.Name == base {
			return a, true
		}
	}
	return Agent{}, false
}

// Names lists the known agents.
func Names() []string {
	out := make([]string, 0, len(known))
	for _, a := range known {
		out = append(out, a.Name)
	}
	return out
}
