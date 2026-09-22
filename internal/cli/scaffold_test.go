package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jrderuiter/nn/internal/config"
)

// init in an empty project writes an empty file, and that file has to load.
func TestEnsureConfigWritesAnEmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := ensureConfig(options{workdir: dir}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, config.FileName)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected a generated %s: %v", config.FileName, err)
	}
	if len(got) != 0 {
		t.Errorf("expected an empty file, got:\n%s", got)
	}
	if _, err := config.Load(config.Options{Dir: dir, Explicit: path}); err != nil {
		t.Fatalf("the generated file must load: %v", err)
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

// Every command but init refuses to run without a project file, and writes
// nothing.
func TestRequireConfigFailsWithoutAFile(t *testing.T) {
	dir := t.TempDir()
	if err := requireConfig(options{workdir: dir}); !errors.Is(err, errNoConfig) {
		t.Fatalf("expected errNoConfig, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, config.FileName)); err == nil {
		t.Error("a missing configuration must not be created")
	}
}

// The project file above a subdirectory counts, as it does for the loader.
func TestRequireConfigFindsTheProjectFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, config.FileName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "service")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := requireConfig(options{workdir: sub}); err != nil {
		t.Fatal(err)
	}
}
