package cli

import "path/filepath"

// knownAgents are the commands that `nn` recognizes as an agent.
//
// The name is used for HERDR_AGENT only. It selects no profile and grants
// nothing: the sandbox comes from nn.toml, so the same project gives the same
// profile whatever command runs inside it.
var knownAgents = map[string]bool{
	"claude": true,
	"codex":  true,
}

// agentName returns the agent that a command runs, or an empty string when the
// command is not one. It matches on the base name, so an absolute path such as
// ~/.local/bin/claude still resolves.
func agentName(command []string) string {
	if len(command) == 0 {
		return ""
	}
	base := filepath.Base(command[0])
	if !knownAgents[base] {
		return ""
	}
	return base
}
