package nono

import (
	"strings"
	"testing"
)

func TestRunArgs(t *testing.T) {
	got := RunArgs{
		ProfilePath: "/p/.nono/nn/profile.json",
		Workdir:     "/p",
		Command:     []string{"claude", "--verbose"},
	}.Build()
	want := "run -s --no-diagnostics --workdir /p --allow-cwd --profile /p/.nono/nn/profile.json -- claude --verbose"
	if strings.Join(got, " ") != want {
		t.Fatalf("got:  %s\nwant: %s", strings.Join(got, " "), want)
	}
}

// Without --allow-cwd a non-interactive run fails with
// "CWD access requires --allow-cwd", so the flag must always be present.
func TestRunArgsAlwaysAllowsTheWorkingDirectory(t *testing.T) {
	got := RunArgs{ProfilePath: "/p/profile.json", Command: []string{"true"}}.Build()
	found := false
	for _, a := range got {
		if a == "--allow-cwd" {
			found = true
		}
	}
	if !found {
		t.Fatalf("--allow-cwd is missing from %v", got)
	}
}

// Everything after -- belongs to the child, including flags that nono itself
// would otherwise read.
func TestRunArgsKeepsChildFlagsAfterTheSeparator(t *testing.T) {
	got := RunArgs{ProfilePath: "/p.json", Command: []string{"kubectl", "--context", "x"}}.Build()
	joined := strings.Join(got, " ")
	sep := strings.Index(joined, " -- ")
	if sep < 0 {
		t.Fatal("the separator is missing")
	}
	if strings.Contains(joined[:sep], "--context") {
		t.Fatal("a child flag leaked into nono's own arguments")
	}
}

// A few profile fields resolve $WORKDIR from the environment instead of
// expanding it, so nono itself has to see the variable.
func TestEnvSetsWorkdir(t *testing.T) {
	got := Env("/p", "", nil)
	found := 0
	for _, kv := range got {
		if kv == "WORKDIR=/p" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("expected exactly one WORKDIR entry, got %d", found)
	}
}

// An existing value must not survive alongside the one nn sets, or the child
// inherits two and the winner depends on the platform.
func TestEnvReplacesAnExistingWorkdir(t *testing.T) {
	t.Setenv("WORKDIR", "/stale")
	for _, kv := range Env("/p", "", nil) {
		if kv == "WORKDIR=/stale" {
			t.Fatal("the stale value is still present")
		}
	}
}

// nono's capability table and denial report are hidden by default, because
// they appear in front of whatever the user actually ran.
func TestRunArgsQuietByDefault(t *testing.T) {
	got := strings.Join(RunArgs{ProfilePath: "/p.json", Command: []string{"true"}}.Build(), " ")
	for _, want := range []string{"-s", "--no-diagnostics"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %s in %s", want, got)
		}
	}
}

func TestRunArgsCanShowBothAgain(t *testing.T) {
	got := strings.Join(RunArgs{
		ProfilePath: "/p.json", Command: []string{"true"},
		Banner: true, Diagnostics: true,
	}.Build(), " ")
	for _, unwanted := range []string{"-s", "--no-diagnostics"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("did not expect %s in %s", unwanted, got)
		}
	}
}

// HERDR_AGENT names the agent that the sandboxed command runs, for tools on
// the host. It is not in allow_vars, so it stops at nono.
func TestEnvSetsTheAgent(t *testing.T) {
	var got string
	for _, kv := range Env("/p", "claude", nil) {
		if strings.HasPrefix(kv, "HERDR_AGENT=") {
			got = kv
		}
	}
	if got != "HERDR_AGENT=claude" {
		t.Fatalf("got %q", got)
	}
}

// An unknown command sets nothing, rather than guessing a name.
func TestEnvOmitsTheAgentWhenUnknown(t *testing.T) {
	for _, kv := range Env("/p", "", nil) {
		if strings.HasPrefix(kv, "HERDR_AGENT=") {
			t.Fatalf("expected no agent, got %q", kv)
		}
	}
}

func TestEnvReplacesAnExistingAgent(t *testing.T) {
	t.Setenv("HERDR_AGENT", "stale")
	count := 0
	for _, kv := range Env("/p", "codex", nil) {
		if strings.HasPrefix(kv, "HERDR_AGENT=") {
			count++
			if kv != "HERDR_AGENT=codex" {
				t.Errorf("got %q", kv)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one entry, got %d", count)
	}
}

// A resolved secret reaches nono through its environment, and nowhere else.
func TestEnvCarriesResolvedSecrets(t *testing.T) {
	got := Env("/p", "claude", []string{"NN_GITHUB_TOKEN=abc123"})
	var found string
	for _, kv := range got {
		if strings.HasPrefix(kv, "NN_GITHUB_TOKEN=") {
			found = kv
		}
	}
	if found != "NN_GITHUB_TOKEN=abc123" {
		t.Fatalf("got %q", found)
	}
}

// A stale host value must not survive next to the resolved one.
func TestEnvReplacesAStaleSecret(t *testing.T) {
	t.Setenv("NN_GITHUB_TOKEN", "stale")
	count := 0
	for _, kv := range Env("/p", "", []string{"NN_GITHUB_TOKEN=fresh"}) {
		if strings.HasPrefix(kv, "NN_GITHUB_TOKEN=") {
			count++
			if kv != "NN_GITHUB_TOKEN=fresh" {
				t.Errorf("got %q", kv)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected one entry, got %d", count)
	}
}
