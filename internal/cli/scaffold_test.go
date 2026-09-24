package cli

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jrderuiter/nn/internal/config"
	"github.com/jrderuiter/nn/internal/tool"
)

// init in an empty project writes the example, and that file has to load.
func TestEnsureConfigWritesTheExample(t *testing.T) {
	dir := t.TempDir()
	if err := ensureConfig(options{workdir: dir}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, config.FileName)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected a generated %s: %v", config.FileName, err)
	}
	if string(got) != example() {
		t.Errorf("expected the example, got:\n%s", got)
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

// The example is written by hand, so this test is what keeps it complete: a
// new tool, or a new setting of one, must appear in its section.
func TestExampleCoversEveryTool(t *testing.T) {
	body := example()
	section := func(name string) string {
		head := "[tools." + name + "]"
		i := strings.Index(body, head)
		if i < 0 {
			return ""
		}
		rest := body[i+len(head):]
		if j := strings.Index(rest, "[tools."); j >= 0 {
			rest = rest[:j]
		}
		return rest
	}
	for _, k := range tool.EnvKeys() {
		name, key, _ := strings.Cut(strings.TrimPrefix(k.Path, "tools."), ".")
		if !strings.Contains(body, "[tools."+name+"]") {
			t.Errorf("the example has no [tools.%s] section", name)
			continue
		}
		if key == "" {
			continue
		}
		setting := regexp.MustCompile(`(?m)^(# )?` + regexp.QuoteMeta(key) + ` = `)
		if !setting.MatchString(section(name)) {
			t.Errorf("the [tools.%s] section of the example does not show %s", name, key)
		}
	}
}
