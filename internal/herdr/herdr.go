// Package herdr sets HERDR_AGENT for sessions launched under the herdr
// terminal workspace manager.
//
// herdr stamps HERDR_* into each pane based on what it launched. When it
// launches nn, it sees "nn" — not the agent nn goes on to exec. nn knows
// which agent it is about to run, so it corrects the variable on the way past.
package herdr

import (
	"os"
	"path/filepath"
	"strings"
)

// EnvVar is the variable herdr reads to identify the agent in a pane.
const EnvVar = "HERDR_AGENT"

// agents maps a binary name to herdr's agent id. Seeded from
// `herdr integration list`; most are identity, a few binaries are named
// differently from the integration.
var agents = map[string]string{
	"claude":          "claude",
	"codex":           "codex",
	"copilot":         "copilot",
	"cursor-agent":    "cursor",
	"cursor":          "cursor",
	"devin":           "devin",
	"droid":           "droid",
	"grok":            "grok",
	"hermes":          "hermes",
	"kilo":            "kilo",
	"kimi":            "kimi",
	"mastracode":      "mastracode",
	"omp":             "omp",
	"opencode":        "opencode",
	"pi":              "pi",
	"qodercli":        "qodercli",
	"qwen":            "qwen",
	"antigravity-cli": "antigravity-cli",
}

// launchers are run-a-package shims. The agent is the argument after them,
// so `npx opencode` should still resolve to opencode.
var launchers = map[string]bool{
	"npx": true, "bunx": true, "pnpm": true, "dlx": true,
	"uvx": true, "yarn": true, "deno": true,
}

// Infer returns the herdr agent id for a command, if it names a known agent.
func Infer(command []string) (string, bool) {
	for _, tok := range command {
		if tok == "" || strings.HasPrefix(tok, "-") {
			continue // a flag, not the program
		}
		name := strings.TrimSuffix(filepath.Base(tok), filepath.Ext(tok))
		if launchers[name] {
			continue
		}
		if id, ok := agents[name]; ok {
			return id, true
		}
		// The first non-launcher token is the program. If it is not a known
		// agent, nothing later in the argv will be either.
		return "", false
	}
	return "", false
}

// Active reports whether this process is running under herdr.
func Active(environ map[string]string) bool {
	return environ["HERDR_ENV"] == "1" || environ["HERDR_SOCKET_PATH"] != ""
}

// Apply sets HERDR_AGENT in env when running under herdr and the command
// names a known agent. It overrides any inherited value: herdr only saw "nn".
func Apply(env map[string]string, command []string, environ map[string]string) {
	if !Active(environ) {
		return
	}
	if id, ok := Infer(command); ok {
		if _, explicit := env[EnvVar]; !explicit {
			env[EnvVar] = id
		}
	}
}

// Environ is a convenience wrapper over the real process environment.
func Environ() map[string]string {
	m := make(map[string]string)
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}
