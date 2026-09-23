package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runProfile runs `nn profile` against one case and returns what it printed.
func runProfile(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	abs := setupCase(t, dir)
	root := newRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{
		"profile", "--workdir", abs, "--config", filepath.Join(abs, "nn.toml"),
	}, args...))
	err := root.Execute()
	return out.String(), err
}

// Without --tool, the command prints the profile that a run uses.
func TestProfilePrintsTheWholeProfile(t *testing.T) {
	got, err := runProfile(t, "testdata/cases/all")
	if err != nil {
		t.Fatal(err)
	}
	got = strings.ReplaceAll(got, opts.workdir, "/TESTDIR")
	want := readGolden(t, "all")
	if got != want {
		t.Errorf("nn profile differs from the golden profile\n--- got ---\n%s", got)
	}
}

// --tool narrows the profile, and the base layer stays.
func TestProfileKeepsOnlyTheSelectedTools(t *testing.T) {
	got, err := runProfile(t, "testdata/cases/all", "--tool", "git")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "GIT_AUTHOR_NAME") || !strings.Contains(got, `"workdir"`) {
		t.Errorf("the git settings or the base layer are missing:\n%s", got)
	}
	assertNoOtherTool(t, got)
}

// --as-mixin prints only what the tools add.
func TestProfileAsMixinLeavesOutTheBase(t *testing.T) {
	got, err := runProfile(t, "testdata/cases/all", "--tool", "git", "--as-mixin")
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(got)) {
		t.Fatalf("the output is not JSON:\n%s", got)
	}
	if !strings.Contains(got, "GIT_AUTHOR_NAME") {
		t.Errorf("the git settings are missing:\n%s", got)
	}
	// The base layer and the [nono] settings belong to a run, not to a mixin.
	for _, base := range []string{`"workdir"`, `"TMPDIR"`, `"extends"`, "$WORKDIR/.nono/nn\""} {
		if strings.Contains(got, base) {
			t.Errorf("the mixin holds %s from the base layer:\n%s", base, got)
		}
	}
	assertNoOtherTool(t, got)
}

func assertNoOtherTool(t *testing.T, got string) {
	t.Helper()
	for _, other := range []string{"github", "azure", "kube", "mise"} {
		if strings.Contains(got, other) {
			t.Errorf("the git profile holds %q from another tool:\n%s", other, got)
		}
	}
}

// A name that nn.toml does not enable is an error, not an empty profile.
func TestProfileRejectsAToolThatIsNotEnabled(t *testing.T) {
	_, err := runProfile(t, "testdata/cases/github", "--tool", "github", "--tool", "kubernetes")
	if err == nil {
		t.Fatal("a tool that nn.toml does not enable must be an error")
	}
	if !strings.Contains(err.Error(), `"kubernetes"`) {
		t.Errorf("the error does not name the tool: %v", err)
	}
}

func readGolden(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata/golden", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
