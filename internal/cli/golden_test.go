package cli

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrderuiter/nn/internal/nono"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// Each case directory under testdata/cases holds an nn.toml and whatever
// fixtures it needs. The golden file is the profile that nn generates for it.
func TestGoldenProfiles(t *testing.T) {
	cases, err := filepath.Glob("testdata/cases/*")
	if err != nil || len(cases) == 0 {
		t.Fatalf("no cases found: %v", err)
	}
	for _, dir := range cases {
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			p := buildCase(t, dir)
			got, err := nono.Marshal(p.profile)
			if err != nil {
				t.Fatal(err)
			}
			assertProfilePathsArePortable(t, p)
			// A credential capture command is host argv, passed through
			// verbatim, so it may carry an absolute path. Normalize it, or the
			// golden file would only match on one machine.
			got = []byte(strings.ReplaceAll(string(got), p.ws.Workdir, "/TESTDIR"))
			golden := filepath.Join("testdata/golden", name+".json")
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden file; run go test ./internal/cli -update: %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("profile differs from %s\n--- got ---\n%s", golden, got)
			}
		})
	}
}

// buildCase runs the pipeline against one case directory with a fixed
// environment, so the output does not depend on the machine.
func buildCase(t *testing.T, dir string) *plan {
	t.Helper()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A fixed home and a fixed proxy port keep the output stable.
	t.Setenv("HOME", filepath.Join(abs, "home"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(abs, "home", ".cache"))
	saved := opts
	t.Cleanup(func() { opts = saved })
	opts = options{
		workdir:    abs,
		configPath: filepath.Join(abs, "nn.toml"),
	}
	p, err := build(context.Background(), opts, []string{"claude"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return p
}

// An unknown name is rejected rather than silently producing an empty profile.
func TestUnknownToolNameIsRejected(t *testing.T) {
	abs, _ := filepath.Abs("testdata/cases/all")
	saved := opts
	t.Cleanup(func() { opts = saved })
	opts = options{
		workdir:    abs,
		configPath: filepath.Join(abs, "nn.toml"),
		only:       []string{"kubernets"},
	}
	if _, err := build(context.Background(), opts, nil); err == nil {
		t.Fatal("a misspelled tool name must be an error")
	}
}

// assertProfilePathsArePortable checks the parts of the profile that nono
// expands itself. Those must stay relative to $WORKDIR, or the profile only
// works on the machine that generated it.
func assertProfilePathsArePortable(t *testing.T, p *plan) {
	t.Helper()
	portable := *p.profile
	// The capture command is host argv and is exempt.
	portable.CredentialCapture = nil
	body, err := nono.Marshal(&portable)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), p.ws.Workdir) {
		t.Errorf("the generated profile leaks an absolute path:\n%s", body)
	}
}
