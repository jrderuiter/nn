package nono

import (
	"strings"
	"testing"

	"github.com/jderuiter/nn/internal/config"
)

func envOf(t *testing.T, src string, cli EnvOverrides, host []string) (*Plan, error) {
	t.Helper()
	return buildYAML(t, src, config.ModeRun, Options{Env: cli, Environ: host})
}

func lookup(env []string, key string) (string, bool) {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v, true
		}
	}
	return "", false
}

func TestEnvOverrideReplacesInPlace(t *testing.T) {
	host := []string{"PATH=/usr/bin", "KEEP=yes", "OVERRIDE=old"}
	p, err := envOf(t, "command: [x]\nenv:\n  OVERRIDE: new\n  FRESH: added\n", EnvOverrides{}, host)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := lookup(p.Env, "OVERRIDE"); v != "new" {
		t.Errorf("OVERRIDE = %q, want new", v)
	}
	if v, _ := lookup(p.Env, "KEEP"); v != "yes" {
		t.Errorf("inherited vars must survive, KEEP = %q", v)
	}
	if v, _ := lookup(p.Env, "FRESH"); v != "added" {
		t.Errorf("FRESH = %q", v)
	}
	// Replacement is in place, so print output shows one entry, not two.
	if n := count(p.Env, "OVERRIDE"); n != 1 {
		t.Errorf("OVERRIDE appears %d times, want 1", n)
	}
}

func TestEnvUnset(t *testing.T) {
	host := []string{"PATH=/usr/bin", "AWS_PROFILE=dev"}
	p, err := envOf(t, "command: [x]\nenv_unset: [AWS_PROFILE]\n", EnvOverrides{}, host)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lookup(p.Env, "AWS_PROFILE"); ok {
		t.Error("AWS_PROFILE should be gone")
	}
	if len(p.EnvRemoved) != 1 || p.EnvRemoved[0] != "AWS_PROFILE" {
		t.Errorf("EnvRemoved = %v", p.EnvRemoved)
	}
}

func TestEnvPrecedence(t *testing.T) {
	host := []string{"PATH=/usr/bin", "K=host"}
	// CLI -e beats env:.
	p, err := envOf(t, "command: [x]\nenv:\n  K: config\n", EnvOverrides{Set: map[string]string{"K": "cli"}}, host)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := lookup(p.Env, "K"); v != "cli" {
		t.Errorf("K = %q, want cli", v)
	}
	// CLI -u beats a config set from any source.
	p, err = envOf(t, "command: [x]\nenv:\n  K: config\n", EnvOverrides{Unset: []string{"K"}}, host)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lookup(p.Env, "K"); ok {
		t.Error("-u must beat env:")
	}
}

func TestEnvExpansion(t *testing.T) {
	host := []string{"HOME=/home/u", "PATH=/usr/bin"}

	p, err := envOf(t, "command: [x]\nenv:\n  P: \"${HOME}/.local/bin:${PATH}\"\n", EnvOverrides{}, host)
	if err != nil {
		t.Fatal(err)
	}
	// ${PATH} resolves against the host, never a half-built override map.
	if v, _ := lookup(p.Env, "P"); v != "/home/u/.local/bin:/usr/bin" {
		t.Errorf("P = %q", v)
	}

	// $$ is a literal dollar.
	p, err = envOf(t, "command: [x]\nenv:\n  D: 'a$$b'\n", EnvOverrides{}, host)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := lookup(p.Env, "D"); v != "a$b" {
		t.Errorf("D = %q, want a$b", v)
	}

	// An unknown variable is an error, not a silent empty string.
	_, err = envOf(t, "command: [x]\nenv:\n  X: \"${NOPE}/y\"\n", EnvOverrides{}, host)
	if err == nil || !strings.Contains(err.Error(), "NOPE") {
		t.Errorf("want an error naming NOPE, got %v", err)
	}

	// nono's own profile tokens get a targeted message.
	_, err = envOf(t, "command: [x]\nenv:\n  X: \"$WORKDIR/y\"\n", EnvOverrides{}, host)
	if err == nil || !strings.Contains(err.Error(), "profile.json") {
		t.Errorf("want the nono-token hint, got %v", err)
	}
}

func TestCLIEnvIsVerbatim(t *testing.T) {
	// The shell already expanded it; a second pass would be a bug.
	host := []string{"HOME=/home/u"}
	p, err := envOf(t, "command: [x]\n", EnvOverrides{Set: map[string]string{"K": "${HOME}"}}, host)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := lookup(p.Env, "K"); v != "${HOME}" {
		t.Errorf("K = %q, want it untouched", v)
	}
}

// Expansion reads only the host env, so map iteration order cannot matter.
func TestEnvOrderIndependence(t *testing.T) {
	src := "command: [x]\nenv:\n  A: '1'\n  B: '2'\n  C: '3'\n  D: '4'\n  E: '5'\n"
	host := []string{"PATH=/usr/bin", "HOME=/h"}
	first, err := envOf(t, src, EnvOverrides{}, host)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(first.Env, "\x00")
	for range 100 {
		p, err := envOf(t, src, EnvOverrides{}, host)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(p.Env, "\x00") != want {
			t.Fatal("env output is not deterministic across runs")
		}
	}
}

func TestHerdrAgentInferred(t *testing.T) {
	underHerdr := []string{"PATH=/usr/bin", "HERDR_ENV=1", "HERDR_AGENT=nn"}

	p, err := envOf(t, "command: [claude]\n", EnvOverrides{}, underHerdr)
	if err != nil {
		t.Fatal(err)
	}
	// herdr only saw `nn`; nn knows better and corrects it.
	if v, _ := lookup(p.Env, "HERDR_AGENT"); v != "claude" {
		t.Errorf("HERDR_AGENT = %q, want claude", v)
	}

	// Not an agent: leave whatever herdr set alone.
	p, err = envOf(t, "command: [go, test]\n", EnvOverrides{}, underHerdr)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := lookup(p.Env, "HERDR_AGENT"); v != "nn" {
		t.Errorf("non-agent command should not set HERDR_AGENT, got %q", v)
	}

	// Outside herdr, do nothing.
	p, err = envOf(t, "command: [claude]\n", EnvOverrides{}, []string{"PATH=/usr/bin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lookup(p.Env, "HERDR_AGENT"); ok {
		t.Error("HERDR_AGENT must not appear outside herdr")
	}

	// An explicit value wins over inference.
	p, err = envOf(t, "command: [claude]\nenv:\n  HERDR_AGENT: custom\n", EnvOverrides{}, underHerdr)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := lookup(p.Env, "HERDR_AGENT"); v != "custom" {
		t.Errorf("explicit env must win, got %q", v)
	}

	// herdr: false opts out.
	p, err = envOf(t, "command: [claude]\nherdr: false\n", EnvOverrides{}, underHerdr)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := lookup(p.Env, "HERDR_AGENT"); v != "nn" {
		t.Errorf("herdr: false should leave it alone, got %q", v)
	}
}

func count(env []string, key string) int {
	n := 0
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); k == key {
			n++
		}
	}
	return n
}
