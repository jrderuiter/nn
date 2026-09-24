package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Prune removes what keep does not name, and never the .gitignore or a
// profile file, because another agent can be running with one.
func TestPruneKeepsProfilesAndClaimedEntries(t *testing.T) {
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.EnsureGitignore(); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"profile.json", "profile-claude.json", "kube/config", "gh/hosts.yml", "az/azuredevops/config",
	} {
		if err := w.Write(rel, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := w.Prune(map[string]bool{"gh": true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"az", "kube"}; !reflect.DeepEqual(removed, want) {
		t.Errorf("removed %v, want %v", removed, want)
	}
	for _, rel := range []string{".gitignore", "profile.json", "profile-claude.json", "gh/hosts.yml"} {
		if _, err := os.Stat(filepath.Join(w.Dir, rel)); err != nil {
			t.Errorf("%s must stay: %v", rel, err)
		}
	}
}

// A project that never wrote anything has nothing to prune.
func TestPruneWithoutADirectory(t *testing.T) {
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	removed, err := w.Prune(nil)
	if err != nil || len(removed) != 0 {
		t.Fatalf("got %v, %v", removed, err)
	}
}
