package git

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/jrderuiter/nn/internal/nono"
	"github.com/jrderuiter/nn/internal/tool"
)

// systemConfig is the only path the git tool writes into the profile. The
// tests compare against it exactly, so any other path is still a failure.
var systemConfig = &nono.Filesystem{ReadFile: []nono.CondPath{nono.PWhen("/etc/gitconfig", "linux")}}

func build(t *testing.T, body string) *tool.Result {
	t.Helper()
	var cfg struct {
		Tools map[string]toml.Primitive `toml:"tools"`
	}
	md, err := toml.Decode("[tools.git]\n"+body, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(md, cfg.Tools["git"])
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Build(context.Background(), &tool.Env{Workdir: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestIdentityNeedsBothHalves(t *testing.T) {
	var cfg struct {
		Tools map[string]toml.Primitive `toml:"tools"`
	}
	md, _ := toml.Decode("[tools.git]\nname = \"Jane\"\n", &cfg)
	if _, err := New(md, cfg.Tools["git"]); err == nil {
		t.Fatal("a name without an email must be an error")
	}
}

// A tool must not pass host variables through for its own settings: set_vars
// reaches the sandbox after the filter.
func TestNoHostVariablePassesThrough(t *testing.T) {
	r := build(t, "name = \"Jane\"\nemail = \"jane@example.com\"\n")
	if len(r.Fragment.Environment.AllowVars) != 0 {
		t.Fatalf("no host variable should pass through, got %v", r.Fragment.Environment.AllowVars)
	}
	if !reflect.DeepEqual(r.Fragment.Filesystem, systemConfig) {
		t.Fatalf("the git tool grants only the system configuration, got %+v", r.Fragment.Filesystem)
	}
	if r.Fragment.Environment.SetVars["GIT_AUTHOR_NAME"] != "Jane" {
		t.Fatal("the identity must still be set")
	}
}

func buildIn(t *testing.T, body, commonDir string) *tool.Result {
	t.Helper()
	var cfg struct {
		Tools map[string]toml.Primitive `toml:"tools"`
	}
	md, err := toml.Decode("[tools.git]\n"+body, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(md, cfg.Tools["git"])
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Build(context.Background(), &tool.Env{
		Workdir:      "/w",
		GitCommonDir: func(context.Context) (string, error) { return commonDir, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The shared git directory is absolute, so it goes to nono as a flag and
// never into the profile.
func TestWorktreeGrantsTheCommonDir(t *testing.T) {
	r := buildIn(t, "", "/repo/.git")
	if got := strings.Join(r.NonoArgs, " "); got != "--allow /repo/.git" {
		t.Fatalf("got %q", got)
	}
	if !reflect.DeepEqual(r.Fragment.Filesystem, systemConfig) {
		t.Fatalf("the grant must not reach the profile, got %+v", r.Fragment.Filesystem)
	}
}

func TestConfigCanBeTurnedOff(t *testing.T) {
	r := build(t, "config = false\n")
	if r.Fragment.Groups != nil || r.Fragment.Filesystem != nil {
		t.Fatalf("no configuration should be readable, got %+v and %+v", r.Fragment.Groups, r.Fragment.Filesystem)
	}
}

func TestWorktreeGrantCanBeTurnedOff(t *testing.T) {
	if r := buildIn(t, "worktree = false\n", "/repo/.git"); len(r.NonoArgs) != 0 {
		t.Fatalf("got %v", r.NonoArgs)
	}
}

func TestNoGrantOutsideAWorktree(t *testing.T) {
	if r := buildIn(t, "", ""); len(r.NonoArgs) != 0 {
		t.Fatalf("got %v", r.NonoArgs)
	}
}
