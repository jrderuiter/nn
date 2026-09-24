package nono

import (
	"strings"
	"testing"
)

func TestMergeAppendsAndDeduplicates(t *testing.T) {
	m := NewMerger(&Profile{
		Filesystem:  &Filesystem{Allow: []CondPath{P("$WORKDIR/.nono/nn")}},
		Environment: &Environment{AllowVars: []string{"PATH", "HOME"}},
	})
	err := m.Add(&Profile{
		Filesystem:  &Filesystem{Allow: []CondPath{P("$WORKDIR/.nono/nn"), P("$HOME/.cache/mise")}},
		Environment: &Environment{AllowVars: []string{"HOME", "GIT_*"}},
	}, "toolchain")
	if err != nil {
		t.Fatal(err)
	}
	got := m.Profile()
	if len(got.Filesystem.Allow) != 2 {
		t.Fatalf("allow should hold 2 unique paths, got %v", got.Filesystem.Allow)
	}
	if len(got.Environment.AllowVars) != 3 {
		t.Fatalf("allow_vars should hold 3 unique names, got %v", got.Environment.AllowVars)
	}
	if got.Environment.AllowVars[2] != "GIT_*" {
		t.Fatalf("first seen order must hold, got %v", got.Environment.AllowVars)
	}
}

func TestMergeConflictingCredentialCapture(t *testing.T) {
	m := NewMerger(&Profile{})
	first := &Profile{CredentialCapture: map[string]CredentialCapture{
		"token": {Command: []string{"fnox", "get", "A"}},
	}}
	second := &Profile{CredentialCapture: map[string]CredentialCapture{
		"token": {Command: []string{"fnox", "get", "B"}},
	}}
	if err := m.Add(first, "github"); err != nil {
		t.Fatal(err)
	}
	err := m.Add(second, "kubernetes")
	if err == nil {
		t.Fatal("two tools defining the same capture differently must be an error")
	}
	for _, want := range []string{"github", "kubernetes", "credential_capture[token]"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should name %q, got: %v", want, err)
		}
	}
}

func TestMergeIdenticalMapEntryIsAllowed(t *testing.T) {
	m := NewMerger(&Profile{})
	same := func() *Profile {
		return &Profile{Environment: &Environment{SetVars: map[string]string{"KUBECONFIG": "/x"}}}
	}
	if err := m.Add(same(), "a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Add(same(), "b"); err != nil {
		t.Fatalf("an identical value must not conflict: %v", err)
	}
}

func TestMergeConflictingScalar(t *testing.T) {
	m := NewMerger(&Profile{})
	if err := m.Add(&Profile{Workdir: &Workdir{Access: "read"}}, "a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Add(&Profile{Workdir: &Workdir{Access: "readwrite"}}, "b"); err == nil {
		t.Fatal("two tools setting workdir.access differently must be an error")
	}
}

func TestMergeNetworkBlockIsSticky(t *testing.T) {
	m := NewMerger(&Profile{})
	yes, no := true, false
	if err := m.Add(&Profile{Network: &Network{Block: &yes}}, "a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Add(&Profile{Network: &Network{Block: &no}}, "b"); err != nil {
		t.Fatal(err)
	}
	if !*m.Profile().Network.Block {
		t.Fatal("a later fragment must not open a network that an earlier one blocked")
	}
}

func TestMergeDomainEndpointsConcatenate(t *testing.T) {
	m := NewMerger(&Profile{})
	add := func(path, name string) {
		err := m.Add(&Profile{Network: &Network{AllowDomain: []Domain{{
			Domain:    "api.github.com",
			Endpoints: []Endpoint{{Method: "GET", Path: path}},
		}}}}, name)
		if err != nil {
			t.Fatal(err)
		}
	}
	add("/user", "a")
	add("/repos/**", "b")
	doms := m.Profile().Network.AllowDomain
	if len(doms) != 1 || len(doms[0].Endpoints) != 2 {
		t.Fatalf("rules for one domain must concatenate, got %+v", doms)
	}
}

// The user's own raw block is the last word, so it replaces what a tool
// set instead of reporting a conflict.
func TestAddOverrideReplaces(t *testing.T) {
	m := NewMerger(&Profile{})
	set := func(v string) *Profile {
		return &Profile{Environment: &Environment{SetVars: map[string]string{"GH_CONFIG_DIR": v}}}
	}
	if err := m.Add(set("$WORKDIR/.nono/nn/gh"), "github"); err != nil {
		t.Fatal(err)
	}
	if err := m.AddOverride(set("/somewhere/else"), "the [nono.profile] block"); err != nil {
		t.Fatalf("the raw block must override, not conflict: %v", err)
	}
	if got := m.Profile().Environment.SetVars["GH_CONFIG_DIR"]; got != "/somewhere/else" {
		t.Fatalf("got %q, want the override to win", got)
	}
	// The override flag must not leak into the next tool.
	if err := m.Add(set("/third"), "git"); err == nil {
		t.Fatal("a later tool must still conflict")
	}
}
