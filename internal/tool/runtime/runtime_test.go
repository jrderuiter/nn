package runtime

import (
	"context"
	"slices"
	"testing"

	"github.com/jrderuiter/nn/internal/tool"
)

// Each runtime tool must name exactly one nono group, so the mapping back to
// nono stays one to one and visible.
func TestEachToolWrapsOneGroup(t *testing.T) {
	for name, sp := range specs {
		if sp.group == "" {
			t.Errorf("tool %q names no nono group", name)
		}
	}
}

func TestBuildIncludesTheGroupAndItsGaps(t *testing.T) {
	p := &provider{name: "go", spec: specs["go"]}
	r, err := p.Build(context.Background(), &tool.Env{})
	if err != nil {
		t.Fatal(err)
	}
	f := r.Fragment
	if len(f.Groups.Include) != 1 || f.Groups.Include[0].Name != "go_runtime" {
		t.Fatalf("expected the go_runtime group, got %+v", f.Groups.Include)
	}
	// go_runtime grants read only, so the writable caches have to come from
	// here or the first build fails on download.
	if len(f.Filesystem.Allow) != 2 {
		t.Fatalf("expected the two writable caches, got %+v", f.Filesystem.Allow)
	}
	// nono drops a grant whose directory does not exist, so they are created.
	if len(r.EnsureDirs) != 2 {
		t.Fatalf("the writable paths must also be created, got %v", r.EnsureDirs)
	}
}

// A tool with nothing to add beyond its group still produces a usable fragment.
func TestToolWithoutGapsIsJustTheGroup(t *testing.T) {
	p := &provider{name: "bun", spec: specs["bun"]}
	r, err := p.Build(context.Background(), &tool.Env{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Fragment.Filesystem != nil || r.Fragment.Environment != nil {
		t.Fatal("bun needs nothing beyond its group")
	}
}

// A platform path is granted everywhere in the profile, but created only on
// its own platform.
func TestEnsureDirsSkipsOtherPlatforms(t *testing.T) {
	allow := specs["mise"].allow
	if got := ensureDirs(allow, "darwin"); len(got) != 3 {
		t.Errorf("macOS must create all three mise paths, got %v", got)
	}
	if got := ensureDirs(allow, "linux"); slices.Contains(got, "$HOME/Library/Caches/mise") || len(got) != 2 {
		t.Errorf("Linux must not create the macOS cache, got %v", got)
	}
}

func TestEveryToolIsRegistered(t *testing.T) {
	known := map[string]bool{}
	for _, n := range tool.Known() {
		known[n] = true
	}
	for _, n := range Names() {
		if !known[n] {
			t.Errorf("tool %q is not registered as a tool", n)
		}
	}
}
