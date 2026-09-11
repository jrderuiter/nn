package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, src string) (*File, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "nn.yml")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

func mustLoad(t *testing.T, src string) *File {
	t.Helper()
	f, err := load(t, src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return f
}

func TestResolveMergesBlockOverTopLevel(t *testing.T) {
	f := mustLoad(t, `
profile: profile.json
allow_cwd: true
trust_proxy_ca: true
command: [claude]
nono_args: [--a]
env:
  SHARED: "1"
  OVERRIDE: top
env_unset: [FROM_TOP]
run:
  skip_dir: [node_modules]
wrap:
  command: [go, test, ./...]
  nono_args: [--b]
  env:
    OVERRIDE: block
  env_unset: [FROM_BLOCK]
`)

	run, dropped := Resolve(f, ModeRun)
	if !run.AllowCwd.Is(true) {
		t.Error("top-level allow_cwd should survive into run")
	}
	if !run.TrustProxyCA.Is(true) {
		t.Error("top-level trust_proxy_ca should survive into run")
	}
	if len(run.SkipDir) != 1 {
		t.Errorf("run block should contribute skip_dir, got %v", run.SkipDir)
	}
	if len(run.Command) != 1 || run.Command[0] != "claude" {
		t.Errorf("run command = %v, want [claude]", run.Command)
	}
	if len(dropped) != 0 {
		t.Errorf("run drops nothing, got %v", dropped)
	}

	wrap, dropped := Resolve(f, ModeWrap)
	// Slices replace rather than append: argv fragments, not capability grants.
	if got := strings.Join(wrap.Command, " "); got != "go test ./..." {
		t.Errorf("wrap command = %q, want replacement", got)
	}
	if got := strings.Join(wrap.NonoArgs, " "); got != "--b" {
		t.Errorf("wrap nono_args = %q, want replacement not append", got)
	}
	// env merges per key; env_unset unions.
	if wrap.Env["SHARED"] != "1" || wrap.Env["OVERRIDE"] != "block" {
		t.Errorf("wrap env = %v, want per-key merge", wrap.Env)
	}
	if got := strings.Join(wrap.EnvUnset, ","); got != "FROM_TOP,FROM_BLOCK" {
		t.Errorf("wrap env_unset = %q, want union", got)
	}
	// trust_proxy_ca is run+shell only, so wrap drops it rather than erroring.
	if wrap.TrustProxyCA.Present {
		t.Error("wrap must not carry trust_proxy_ca")
	}
	if len(dropped) != 1 || dropped[0] != "trust_proxy_ca" {
		t.Errorf("dropped = %v, want [trust_proxy_ca]", dropped)
	}
}

// The asymmetry: a top-level key unsupported by the mode is dropped, but the
// same key inside a mode block is a hard error.
func TestModeBlockStrictTopLevelLenient(t *testing.T) {
	_, err := load(t, "skip_dir: [node_modules]\ncommand: [x]\n")
	if err != nil {
		t.Fatalf("top-level run-only key must load fine: %v", err)
	}

	_, err = load(t, "command: [x]\nwrap:\n  skip_dir: [node_modules]\n")
	if err == nil {
		t.Fatal("run-only key inside wrap: must be an error")
	}
	for _, want := range []string{"skip_dir", "wrap", "available in: run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
}

// A typo in a block you are not invoking should still fail now, not in CI.
func TestValidationCoversAllBlocks(t *testing.T) {
	_, err := load(t, "command: [x]\nwrap:\n  detached: true\n")
	if err == nil {
		t.Fatal("want error for run-only key in wrap block")
	}
}

func TestErrorMessages(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			"profile-expressible key",
			"allow_domain: [api.anthropic.com]\n",
			[]string{"unknown field", "allow_domain", "network.allow_domain", "profile cannot express"},
		},
		{
			"typo",
			"trust_proxy_car: true\n",
			[]string{"did you mean", "trust_proxy_ca"},
		},
		{
			"mode key removed",
			"mode: run\ncommand: [x]\n",
			[]string{"mode", "nn run"},
		},
		{
			"both halves of a pair",
			"command: [x]\nrun:\n  rollback: true\n  no_rollback: true\n",
			[]string{"rollback", "no_rollback"},
		},
		{
			"manifest exclusivity",
			"config: manifest.json\nprofile: p.json\ncommand: [x]\n",
			[]string{"config", "mutually", "profile"},
		},
		{
			"shell takes no command",
			"shell:\n  command: [claude]\n",
			[]string{"shell", "takes no program"},
		},
		{
			"env set and unset",
			"command: [x]\nenv:\n  FOO: '1'\nenv_unset: [FOO]\n",
			[]string{"FOO", "env_unset"},
		},
		{
			"bad env key",
			"command: [x]\nenv:\n  'BAD-KEY': '1'\n",
			[]string{"BAD-KEY"},
		},
		{
			"empty wrapper",
			"command: [x]\nwrappers: [[]]\n",
			[]string{"wrappers[0]", "empty"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := load(t, c.src)
			if err == nil {
				t.Fatal("want error, got nil")
			}
			got := err.Error()
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("error missing %q:\n%s", w, got)
				}
			}
		})
	}
}

func TestManifestAllowsNonSandboxKeys(t *testing.T) {
	if _, err := load(t, "config: m.json\nsilent: true\nverbose: 2\ncommand: [x]\n"); err != nil {
		t.Errorf("non-sandbox keys are fine alongside config: %v", err)
	}
}
