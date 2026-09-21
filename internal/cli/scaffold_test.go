package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jrderuiter/nn/internal/config"
)

// A first run in an empty project writes a file the user can edit, and that
// file has to load as the configuration it claims to be.
func TestEnsureConfigWritesAMinimalFile(t *testing.T) {
	dir := t.TempDir()
	if err := ensureConfig(options{workdir: dir}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, config.FileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected a generated %s: %v", config.FileName, err)
	}
	cfg, err := config.Load(config.Options{Dir: dir, Explicit: path})
	if err != nil {
		t.Fatalf("the generated file must load: %v", err)
	}
	if cfg.Nono.NetworkProfile != "minimal" {
		t.Errorf("expected a narrowed network, got %q", cfg.Nono.NetworkProfile)
	}
	if len(cfg.Nono.Extends) != 1 || cfg.Nono.Extends[0] != "default" {
		t.Errorf("got extends %v", cfg.Nono.Extends)
	}
}

// The existing file is the user's. A second call must not touch it, whatever
// it holds.
func TestEnsureConfigKeepsAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, config.FileName)
	body := "[nono]\nnetwork_profile = \"claude-code\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureConfig(options{workdir: dir}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("the existing configuration was rewritten:\n%s", got)
	}
}

// A file in a subdirectory would shadow the project file above it, so a
// subdirectory of a configured project gets nothing.
func TestEnsureConfigLeavesASubdirectoryAlone(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, config.FileName), []byte("[nono]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "service", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureConfig(options{workdir: sub}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sub, config.FileName)); err == nil {
		t.Error("a subdirectory must not get a second configuration file")
	}
}

// A named path that does not exist is a misspelling, which the loader reports.
// Writing a file there would hide the mistake.
func TestEnsureConfigSkipsAnExplicitPath(t *testing.T) {
	dir := t.TempDir()
	named := filepath.Join(dir, "elsewhere.toml")
	if err := ensureConfig(options{workdir: dir, configPath: named}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{named, filepath.Join(dir, config.FileName)} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("--config must write nothing, but %s exists", p)
		}
	}
}
