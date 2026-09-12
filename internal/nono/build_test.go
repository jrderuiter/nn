package nono

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/jderuiter/nn/internal/config"
	"github.com/jderuiter/nn/internal/discover"
)

var testDirs = discover.Dirs{
	ConfigPath: "/proj/.nono/nn.yml",
	ConfigDir:  "/proj/.nono",
	Root:       "/proj",
}

// buildYAML is the whole pipeline: parse, validate, resolve, assemble.
func buildYAML(t *testing.T, src string, mode config.Mode, opts Options) (*Plan, error) {
	t.Helper()
	var f config.File
	if err := yaml.UnmarshalWithOptions([]byte(src), &f, yaml.DisallowUnknownField()); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := config.Validate(&f); err != nil {
		return nil, err
	}
	sec, dropped := config.Resolve(&f, mode)
	if opts.Environ == nil {
		opts.Environ = []string{"PATH=/usr/bin", "HOME=/home/u"}
	}
	return Build(sec, mode, testDirs, dropped, opts)
}

func argv(t *testing.T, src string, mode config.Mode, opts Options) []string {
	t.Helper()
	p, err := buildYAML(t, src, mode, opts)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return p.Argv
}

func TestBuildMinimal(t *testing.T) {
	src := "profile: profile.json\ncommand: [claude]\n"
	want := []string{"nono", "run", "--profile", "/proj/.nono/profile.json", "--", "claude"}
	if got := argv(t, src, config.ModeRun, Options{}); !eq(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

func TestBuildFullChain(t *testing.T) {
	src := `
profile: profile.json
allow_cwd: true
wrappers:
  - [fnox, exec, --]
command: [claude]
run:
  trust_proxy_ca: true
  no_diagnostics: true
  skip_dir: [node_modules, target]
  verbose: 2
`
	want := []string{
		"fnox", "exec", "--",
		"nono", "run",
		"--profile", "/proj/.nono/profile.json",
		"--allow-cwd",
		"--trust-proxy-ca",
		"--skip-dir", "node_modules", "--skip-dir", "target",
		"--no-diagnostics",
		"--verbose", "--verbose",
		"--", "claude",
	}
	if got := argv(t, src, config.ModeRun, Options{}); !eq(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

func TestModes(t *testing.T) {
	src := `
profile: profile.json
trust_proxy_ca: true
command: [claude]
shell:
  shell_bin: /bin/zsh
wrap:
  command: [go, test, ./...]
`
	// shell takes no trailing command, and uses --shell instead.
	got := argv(t, src, config.ModeShell, Options{})
	if containsArg(got, "--") {
		t.Errorf("shell must not emit a -- separator: %v", got)
	}
	if !containsSeq(got, []string{"--shell", "/bin/zsh"}) {
		t.Errorf("shell missing --shell: %v", got)
	}
	if containsArg(got, "claude") {
		t.Errorf("shell must not carry a command: %v", got)
	}

	// wrap drops trust_proxy_ca (run+shell only) rather than erroring.
	p, err := buildYAML(t, src, config.ModeWrap, Options{})
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if containsArg(p.Argv, "--trust-proxy-ca") {
		t.Errorf("wrap must not emit --trust-proxy-ca: %v", p.Argv)
	}
	if len(p.Dropped) != 1 || p.Dropped[0] != "trust_proxy_ca" {
		t.Errorf("Dropped = %v, want [trust_proxy_ca]", p.Dropped)
	}
	if !containsSeq(p.Argv, []string{"--", "go", "test", "./..."}) {
		t.Errorf("wrap command: %v", p.Argv)
	}
}

func TestCommandOverride(t *testing.T) {
	src := "command: [claude]\n"

	got := argv(t, src, config.ModeRun, Options{Extra: []string{"go", "test"}})
	if !containsSeq(got, []string{"--", "go", "test"}) || containsArg(got, "claude") {
		t.Errorf("-- args must replace the command: %v", got)
	}

	got = argv(t, src, config.ModeRun, Options{Extra: []string{"--resume"}, AppendExtra: true})
	if !containsSeq(got, []string{"--", "claude", "--resume"}) {
		t.Errorf("--append must extend the command: %v", got)
	}

	// A command beginning with a dash is why the -- is unconditional.
	got = argv(t, "command: [\"--version\"]\n", config.ModeRun, Options{})
	if !containsSeq(got, []string{"--", "--version"}) {
		t.Errorf("dash-leading command: %v", got)
	}
}

func TestEmptyCommandIsAnError(t *testing.T) {
	if _, err := buildYAML(t, "profile: p.json\n", config.ModeRun, Options{}); err == nil {
		t.Fatal("want an error when there is no command")
	}
}

func TestKinds(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"switch true", "command: [x]\nallow_cwd: true\n", "--allow-cwd"},
		{"int zero is not absent", "command: [x]\nrun:\n  startup_timeout: 0\n", "--startup-timeout 0"},
		{"string", "command: [x]\nrun:\n  memory: 2G\n", "--memory 2G"},
		{"slice repeats the flag", "command: [x]\nextends: [a, b]\n", "--extends a --extends b"},
		{"count sugar", "command: [x]\nverbose: true\n", "--verbose"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := strings.Join(argv(t, c.src, config.ModeRun, Options{}), " ")
			if !strings.Contains(got, c.want) {
				t.Errorf("got %q, want it to contain %q", got, c.want)
			}
		})
	}

	// An explicit false emits nothing, so a CLI override can suppress a
	// config's true.
	got := strings.Join(argv(t, "command: [x]\nallow_cwd: false\n", config.ModeRun, Options{}), " ")
	if strings.Contains(got, "--allow-cwd") {
		t.Errorf("explicit false must emit nothing: %q", got)
	}
	// An unset switch likewise.
	got = strings.Join(argv(t, "command: [x]\n", config.ModeRun, Options{}), " ")
	if strings.Contains(got, "--allow-cwd") {
		t.Errorf("unset switch must emit nothing: %q", got)
	}
}

func TestPathAnchoring(t *testing.T) {
	src := `
command: [x]
profile: profile.json
workdir: .
bypass_protection: [sub/file]
run:
  skip_dir: [node_modules]
  log_file: logs/nn.log
`
	got := strings.Join(argv(t, src, config.ModeRun, Options{}), " ")
	// profile anchors to .nono; everything else to the project root.
	for _, want := range []string{
		"--profile /proj/.nono/profile.json",
		"--workdir /proj",
		"--bypass-protection /proj/sub/file",
		"--log-file /proj/logs/nn.log",
		"--skip-dir node_modules", // a directory NAME, not a path
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestProfileNamePassthrough(t *testing.T) {
	got := strings.Join(argv(t, "command: [x]\nprofile: nolabs-ai/claude\n", config.ModeRun, Options{}), " ")
	if !strings.Contains(got, "--profile nolabs-ai/claude") {
		t.Errorf("registry profile name must pass through: %s", got)
	}
}

func TestWrappers(t *testing.T) {
	src := "command: [x]\nwrappers: [[mise, exec, --], [fnox, exec, --]]\n"
	got := argv(t, src, config.ModeRun, Options{})
	want := []string{"mise", "exec", "--", "fnox", "exec", "--", "nono", "run"}
	if !containsSeq(got, want) {
		t.Errorf("wrapper chain: %v", got)
	}

	got = argv(t, src, config.ModeRun, Options{NoWrappers: true})
	if got[0] != "nono" {
		t.Errorf("--no-wrappers should leave nono first: %v", got)
	}
}

func TestNonoArgsComeAfterTypedFlags(t *testing.T) {
	src := "command: [x]\nallow_cwd: true\nnono_args: [--allow-file, /tmp/x]\n"
	got := argv(t, src, config.ModeRun, Options{})
	ai, ni := indexOf(got, "--allow-cwd"), indexOf(got, "--allow-file")
	if ai < 0 || ni < 0 || ni < ai {
		t.Errorf("nono_args must follow typed flags: %v", got)
	}
}

// Placing a key in a mode block asserts it belongs to that mode, so a
// mismatch is rejected; the same key at the top level is filtered instead.
// Generated across the whole table so the matrix cannot drift untested.
func TestModeMatrixEnforcement(t *testing.T) {
	for _, spec := range config.Specs {
		for _, m := range []config.Mode{config.ModeRun, config.ModeShell, config.ModeWrap} {
			if spec.Modes&m != 0 {
				continue
			}
			value := sampleValue(spec)

			t.Run("block/"+spec.Key+"/"+m.String(), func(t *testing.T) {
				src := "command: [x]\n" + m.String() + ":\n  " + spec.Key + ": " + value + "\n"
				if _, err := buildYAML(t, src, m, Options{}); err == nil {
					t.Errorf("%s in %s: block must be rejected", spec.Key, m)
				}
			})

			t.Run("toplevel/"+spec.Key+"/"+m.String(), func(t *testing.T) {
				src := "command: [x]\n" + spec.Key + ": " + value + "\n"
				p, err := buildYAML(t, src, m, Options{})
				if err != nil {
					t.Fatalf("%s at top level must be dropped, not rejected: %v", spec.Key, err)
				}
				if containsArg(p.Argv, spec.Flag) {
					t.Errorf("%s must not reach `nono %s`: %v", spec.Flag, m, p.Argv)
				}
				if !contains(p.Dropped, spec.Key) {
					t.Errorf("%s should be reported as dropped, got %v", spec.Key, p.Dropped)
				}
			})
		}
	}
}

func sampleValue(spec config.FlagSpec) string {
	switch spec.Kind {
	case config.KindSwitch:
		return "true"
	case config.KindInt, config.KindCount:
		return "1"
	case config.KindStringSlice:
		return "[a]"
	default:
		return "v"
	}
}

// --- helpers ---

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(s []string, v string) bool { return indexOf(s, v) >= 0 }

func containsArg(s []string, v string) bool { return indexOf(s, v) >= 0 }

func indexOf(s []string, v string) int {
	for i := range s {
		if s[i] == v {
			return i
		}
	}
	return -1
}

func containsSeq(hay, needle []string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if eq(hay[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}

// A profile can name the program itself, via its `binary` key. An explicit
// empty command says so; nono warns if both are given.
func TestExplicitEmptyCommandDefersToProfile(t *testing.T) {
	got := argv(t, "profile: p.json\ncommand: []\n", config.ModeRun, Options{})
	if containsArg(got, "--") {
		t.Errorf("empty command should emit no separator: %v", got)
	}
	want := []string{"nono", "run", "--profile", "/proj/.nono/p.json"}
	if !eq(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}

	// It is still overridable from the CLI.
	got = argv(t, "profile: p.json\ncommand: []\n", config.ModeRun, Options{Extra: []string{"echo", "hi"}})
	if !containsSeq(got, []string{"--", "echo", "hi"}) {
		t.Errorf("CLI override should still work: %v", got)
	}

	// An absent command is still an error: nothing to run and nothing said.
	if _, err := buildYAML(t, "profile: p.json\n", config.ModeRun, Options{}); err == nil {
		t.Error("absent command must still be an error")
	}
}

func TestHerdrAgentFromProfileBinary(t *testing.T) {
	root := t.TempDir()
	nonoDir := filepath.Join(root, ".nono")
	if err := os.MkdirAll(nonoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(nonoDir, "profile.json")
	if err := os.WriteFile(profile, []byte(`{"meta":{"name":"x"},"binary":"claude"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var f config.File
	src := "profile: profile.json\ncommand: []\n"
	if err := yaml.Unmarshal([]byte(src), &f); err != nil {
		t.Fatal(err)
	}
	sec, dropped := config.Resolve(&f, config.ModeRun)
	dirs := discover.Dirs{ConfigDir: nonoDir, Root: root}

	p, err := Build(sec, config.ModeRun, dirs, dropped, Options{
		Environ: []string{"PATH=/usr/bin", "HERDR_ENV=1", "HERDR_AGENT=nn"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.EnvApplied["HERDR_AGENT"]; got != "claude" {
		t.Errorf("HERDR_AGENT = %q, want claude inferred from the profile's binary key", got)
	}
}
