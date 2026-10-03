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
// A case with [agents.<name>] sections also gets one golden file per agent,
// named <case>.<agent>.json.
func TestGoldenProfiles(t *testing.T) {
	cases, err := filepath.Glob("testdata/cases/*")
	if err != nil || len(cases) == 0 {
		t.Fatalf("no cases found: %v", err)
	}
	for _, dir := range cases {
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			p := buildCase(t, dir, "")
			assertGolden(t, p, name)
			for _, agent := range agentNames(p.cfg) {
				assertGolden(t, buildCase(t, dir, agent), name+"."+agent)
			}
		})
	}
}

func assertGolden(t *testing.T, p *plan, name string) {
	t.Helper()
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
}

// buildCase runs the pipeline against one case directory with a fixed
// environment, so the output does not depend on the machine. The agent names
// the [agents.<name>] section to apply, or none when it is empty.
func buildCase(t *testing.T, dir, agent string) *plan {
	t.Helper()
	o := caseOptions(t, dir)
	o.agent = agent
	p, err := build(context.Background(), o, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return p
}

// caseOptions fixes the environment for one case directory and returns the
// options that point at it.
func caseOptions(t *testing.T, dir string) options {
	t.Helper()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A fixed home and a fixed proxy port keep the output stable.
	t.Setenv("HOME", filepath.Join(abs, "home"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(abs, "home", ".cache"))
	// A case cannot hold a real .git directory, because git does not commit
	// one, so the remotes come from a fixture file with one URL per line.
	return options{
		workdir:    abs,
		configPath: filepath.Join(abs, "nn.toml"),
		gitRemotes: func(context.Context, string) ([]string, error) {
			body, err := os.ReadFile(filepath.Join(abs, "remotes"))
			if err != nil {
				return nil, nil
			}
			return strings.Fields(string(body)), nil
		},
		// The case directories live in this repository, which may itself be
		// a linked worktree, and the answer must not depend on that.
		gitCommonDir: func(context.Context, string) (string, error) {
			return "", nil
		},
	}
}

// An unknown name is rejected rather than silently producing an empty profile.
func TestUnknownToolNameIsRejected(t *testing.T) {
	abs, _ := filepath.Abs("testdata/cases/all")
	o := options{
		workdir:    abs,
		configPath: filepath.Join(abs, "nn.toml"),
		only:       []string{"kubernets"},
	}
	if _, err := build(context.Background(), o, nil); err == nil {
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

// allow_domain lets a project name hosts that no tool asks for.
func TestAllowDomainFromTheConfiguration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nn.toml")
	body := "[nono]\nnetwork_profile = \"minimal\"\nallow_domain = [\"proxy.golang.org\"]\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := build(context.Background(), options{workdir: dir, configPath: path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := p.profile.Network
	if n == nil || n.NetworkProfile != "minimal" {
		t.Fatalf("got %+v", n)
	}
	if len(n.AllowDomain) != 1 || n.AllowDomain[0].Domain != "proxy.golang.org" {
		t.Fatalf("got %+v", n.AllowDomain)
	}
}

// A credential route means nono intercepts TLS, and a Go client on macOS reads
// the system trust store rather than the variables nono sets. Without the flag
// such a client cannot verify the connection.
//
// Both branches run on any machine, because the flag is fatal on the platform
// that does not want it: nono rejects an argument it does not define.
func TestTrustFlagFollowsTheCredentialRoutes(t *testing.T) {
	write := func(t *testing.T, goos, body string) *plan {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "nn.toml")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		p, err := build(context.Background(), options{workdir: dir, configPath: path, goos: goos}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	withRoute := strings.Join(write(t, "darwin", "[tools.github]\n").runArgs(), " ")
	if !strings.Contains(withRoute, "--trust-proxy-ca") {
		t.Errorf("a run with a credential route needs the flag: %s", withRoute)
	}

	noRoute := strings.Join(write(t, "darwin", "[tools.mise]\n").runArgs(), " ")
	if strings.Contains(noRoute, "--trust-proxy-ca") {
		t.Errorf("a run with no route must not ask to change the trust store: %s", noRoute)
	}

	// Where Go reads the trust bundle variables that nono sets, the flag is
	// not merely unnecessary. nono does not define it, and refuses to start.
	elsewhere := strings.Join(write(t, "linux", "[tools.github]\n").runArgs(), " ")
	if strings.Contains(elsewhere, "--trust-proxy-ca") {
		t.Errorf("the flag must not be passed where nono has no such argument: %s", elsewhere)
	}
}
