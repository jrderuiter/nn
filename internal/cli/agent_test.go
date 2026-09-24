package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrderuiter/nn/internal/config"
)

func TestAgentName(t *testing.T) {
	cases := map[string]string{
		"claude":                      "claude",
		"codex":                       "codex",
		"agy":                         "agy",
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

func TestSelectAgent(t *testing.T) {
	cfg := &config.Config{Agents: map[string]config.Agent{"claude": {}, "agy": {}}}
	cases := []struct {
		flag    string
		command []string
		want    string
	}{
		{"", []string{"claude"}, "claude"},
		{"", []string{"/Users/me/.local/bin/agy", "--flag"}, "agy"},
		// A command with no section gets the shared profile only.
		{"", []string{"kubectl", "cluster-info"}, ""},
		// codex is a known agent, but it has no section here.
		{"", []string{"codex"}, ""},
		{"", nil, ""},
		// The flag wins over the command.
		{"claude", []string{"bash"}, "claude"},
		{"agy", []string{"claude"}, "agy"},
	}
	for _, c := range cases {
		got, err := selectAgent(cfg, c.flag, c.command)
		if err != nil {
			t.Errorf("selectAgent(%q, %v): %v", c.flag, c.command, err)
			continue
		}
		if got != c.want {
			t.Errorf("selectAgent(%q, %v) = %q, want %q", c.flag, c.command, got, c.want)
		}
	}
}

// A named agent without a section must stop the run, or a typo would start
// the command without its pack.
func TestSelectAgentRejectsAnUnknownFlag(t *testing.T) {
	cfg := &config.Config{Agents: map[string]config.Agent{"claude": {}}}
	_, err := selectAgent(cfg, "cluade", []string{"bash"})
	if err == nil || !strings.Contains(err.Error(), "cluade") {
		t.Fatalf("got %v", err)
	}
}

// Each agent writes its own profile, so two agents in one project do not
// overwrite each other.
func TestEachAgentHasItsOwnProfileFile(t *testing.T) {
	abs, _ := filepath.Abs("testdata/cases/agents")
	shared := buildCase(t, abs, "")
	claude := buildCase(t, abs, "claude")
	agy := buildCase(t, abs, "agy")
	paths := map[string]bool{
		shared.ws.ProfilePath(shared.agent): true,
		claude.ws.ProfilePath(claude.agent): true,
		agy.ws.ProfilePath(agy.agent):       true,
	}
	if len(paths) != 3 {
		t.Fatalf("the profiles share a file: %v", paths)
	}
	if !strings.Contains(strings.Join(agy.runArgs(), " "), "profile-agy.json") {
		t.Errorf("nono does not get the agent profile: %v", agy.runArgs())
	}
}
