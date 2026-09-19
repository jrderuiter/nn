package cli

import "testing"

func TestAgentName(t *testing.T) {
	cases := map[string]string{
		"claude":                      "claude",
		"codex":                       "codex",
		"/Users/me/.local/bin/claude": "claude",
		"kubectl":                     "",
		"npm":                         "",
		"":                            "",
	}
	for command, want := range cases {
		var argv []string
		if command != "" {
			argv = []string{command, "--flag"}
		}
		if got := agentName(argv); got != want {
			t.Errorf("agentName(%q) = %q, want %q", command, got, want)
		}
	}
}

// The agent name must not change the sandbox. It only names the command for
// HERDR_AGENT, so the same project gives the same profile whatever runs in it.
func TestAgentNameGrantsNothing(t *testing.T) {
	for name := range knownAgents {
		if name == "" {
			t.Fatal("an empty agent name would set an empty HERDR_AGENT")
		}
	}
}

// HERDR_AGENT is for the host side. It must not be in the list that lets a
// variable through to the sandboxed command.
func TestAgentVariableDoesNotReachTheSandbox(t *testing.T) {
	for _, v := range baseAllowVars {
		if v == "HERDR_AGENT" {
			t.Fatal("HERDR_AGENT must not be allowed into the sandbox")
		}
	}
}
