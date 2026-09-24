package cli

import (
	"strings"
	"testing"

	"github.com/jrderuiter/nn/internal/nono"
	"github.com/jrderuiter/nn/internal/tool"
)

// Entries from every tool share one numbered list, in the order given, so a
// repeated key such as an insteadOf rewrite keeps each of its values.
func TestGitConfigIsNumberedOnce(t *testing.T) {
	vars := gitConfigFragment([]tool.GitConfig{
		{Key: "url.a.insteadOf", Value: "x"},
		{Key: "url.a.insteadOf", Value: "y"},
		{Key: "url.b.insteadOf", Value: "z"},
	}).Environment.SetVars
	want := map[string]string{
		"GIT_CONFIG_COUNT":   "3",
		"GIT_CONFIG_KEY_0":   "url.a.insteadOf",
		"GIT_CONFIG_VALUE_0": "x",
		"GIT_CONFIG_KEY_1":   "url.a.insteadOf",
		"GIT_CONFIG_VALUE_1": "y",
		"GIT_CONFIG_KEY_2":   "url.b.insteadOf",
		"GIT_CONFIG_VALUE_2": "z",
	}
	if len(vars) != len(want) {
		t.Fatalf("got %v", vars)
	}
	for k, v := range want {
		if vars[k] != v {
			t.Errorf("%s = %q, want %q", k, vars[k], v)
		}
	}
}

// Only the named grants go. The rest keep their order, so the written profile
// stays byte stable.
func TestDropGrantsKeepsTheRest(t *testing.T) {
	p := &nono.Profile{Filesystem: &nono.Filesystem{Allow: []nono.CondPath{
		nono.P("$WORKDIR/.nono/nn"),
		nono.P("$XDG_CACHE_HOME/go-build"),
		nono.P("$HOME/go"),
	}}}
	dropGrants(p, map[string]bool{"$XDG_CACHE_HOME/go-build": true})
	var got []string
	for _, c := range p.Filesystem.Allow {
		got = append(got, c.Path)
	}
	want := []string{"$WORKDIR/.nono/nn", "$HOME/go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// A directory counts as used when an artifact lives in it, or when the profile
// points a program at it without an artifact, as GH_CONFIG_DIR does.
func TestUsedEntriesReadsArtifactsAndProfile(t *testing.T) {
	profile := []byte(`{"environment":{"set_vars":{"GH_CONFIG_DIR":"$WORKDIR/.nono/nn/gh"}},` +
		`"filesystem":{"allow":["$WORKDIR/.nono/nn"]}}`)
	got := usedEntries([]tool.Artifact{{RelPath: "kube/config"}}, profile)
	want := map[string]bool{"kube": true, "gh": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("%s is missing from %v", k, got)
		}
	}
}
