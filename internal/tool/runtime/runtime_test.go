package runtime

import (
	"context"
	"testing"

	"github.com/BurntSushi/toml"

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

func buildMise(t *testing.T, body string) *tool.Result {
	t.Helper()
	var cfg struct {
		Tools map[string]toml.Primitive `toml:"tools"`
	}
	md, err := toml.Decode("[tools.mise]\n"+body, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := factoryFor("mise")(md, cfg.Tools["mise"])
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Build(context.Background(), &tool.Env{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The trusted path is the profile spelling of the working directory, so the
// profile stays portable.
func TestMiseTrustsTheWorkdir(t *testing.T) {
	r := buildMise(t, "trust_workdir = true\n")
	if got := r.Fragment.Environment.SetVars["MISE_TRUSTED_CONFIG_PATHS"]; got != "$WORKDIR" {
		t.Fatalf("got %q", got)
	}
}

func TestMiseTrustsNothingByDefault(t *testing.T) {
	r := buildMise(t, "")
	if r.Fragment.Environment != nil {
		t.Fatalf("got %+v", r.Fragment.Environment)
	}
}
