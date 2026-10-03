//go:build !windows

package nono

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fakeNono puts a nono on the PATH that runs the given shell script.
func fakeNono(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\n" + script + "\n"
	if err := os.WriteFile(filepath.Join(dir, Binary), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestHerdrStartsNonoAsAChild(t *testing.T) {
	fakeNono(t, "exit 7")
	t.Setenv("HERDR_ENV", "1")
	code, err := launch(nil, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 {
		t.Fatalf("got exit code %d, want 7", code)
	}
}

func TestHerdrReportsAKillingSignal(t *testing.T) {
	fakeNono(t, "kill -TERM $$")
	t.Setenv("HERDR_ENV", "1")
	code, err := launch(nil, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if code != 143 {
		t.Fatalf("got exit code %d, want 143", code)
	}
}

// Outside herdr, nono replaces nn. The test runs the launch in a copy of the
// test binary, and the fake nono prints its own pid. With exec it is the pid
// of that copy.
func TestOutsideHerdrNonoReplacesNn(t *testing.T) {
	if os.Getenv("NN_TEST_LAUNCH") == "1" {
		os.Stdout.WriteString(strconv.Itoa(os.Getpid()) + "\n")
		_, err := launch(nil, os.Environ())
		t.Fatalf("launch returned: %v", err)
	}
	fakeNono(t, "echo $$")
	cmd := exec.Command(os.Args[0], "-test.run=^TestOutsideHerdrNonoReplacesNn$")
	cmd.Env = append(os.Environ(), "NN_TEST_LAUNCH=1", "HERDR_ENV=")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	lines := strings.Fields(string(out))
	if len(lines) != 2 || lines[0] != lines[1] {
		t.Fatalf("expected the same pid twice, got %q", out)
	}
}

func TestWithAgentAddsTheAgent(t *testing.T) {
	got, changed := withAgent([]string{"PATH=/bin"}, "claude")
	if !changed || !slices.Equal(got, []string{"PATH=/bin", "HERDR_AGENT=claude"}) {
		t.Fatalf("got %v, %v", got, changed)
	}
}

// The copy of nn that already has the right value must not exec again, or nn
// loops.
func TestWithAgentLeavesTheRightValue(t *testing.T) {
	in := []string{"HERDR_AGENT=claude", "PATH=/bin"}
	got, changed := withAgent(in, "claude")
	if changed || !slices.Equal(got, in) {
		t.Fatalf("got %v, %v", got, changed)
	}
}

func TestWithAgentReplacesAStaleValue(t *testing.T) {
	got, changed := withAgent([]string{"HERDR_AGENT=codex", "PATH=/bin"}, "claude")
	if !changed || !slices.Equal(got, []string{"PATH=/bin", "HERDR_AGENT=claude"}) {
		t.Fatalf("got %v, %v", got, changed)
	}
}

// A command that is not an agent must not inherit a stale name.
func TestWithAgentRemovesAStaleValueForNoAgent(t *testing.T) {
	got, changed := withAgent([]string{"HERDR_AGENT=claude", "PATH=/bin"}, "")
	if !changed || !slices.Equal(got, []string{"PATH=/bin"}) {
		t.Fatalf("got %v, %v", got, changed)
	}
}
