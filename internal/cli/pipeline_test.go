package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrderuiter/nn/internal/config"
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

// A linked worktree reports the git directory of the main repository. The
// main checkout reports nothing, because its .git is in the working directory.
func TestGitCommonDirFindsTheMainRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(tmp, "repo")
	wt := filepath.Join(tmp, "wt")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = tmp
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", repo)
	run("-C", repo, "commit", "-q", "--allow-empty", "-m", "init")
	run("-C", repo, "worktree", "add", "-q", wt)

	ctx := context.Background()
	if got, _ := gitCommonDir(ctx, wt); got != filepath.Join(repo, ".git") {
		t.Errorf("worktree: got %q", got)
	}
	if got, _ := gitCommonDir(ctx, repo); got != "" {
		t.Errorf("main checkout: got %q", got)
	}
	if got, _ := gitCommonDir(ctx, tmp); got != "" {
		t.Errorf("not a repository: got %q", got)
	}
}

// On Linux a socket lets the agent act outside the sandbox, so the block is
// on unless the user's own [nono.profile] block turns it off.
func TestUnixSocketsAreBlockedByDefault(t *testing.T) {
	profileFor := func(t *testing.T, body string) *nono.Profile {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "nn.toml")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		p, err := build(context.Background(), options{workdir: dir, configPath: path}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return p.profile
	}

	if got := profileFor(t, "[tools.git]\n").Linux; got == nil || got.AfUnixMediation != "pathname" {
		t.Errorf("expected af_unix_mediation = pathname, got %+v", got)
	}
	off := profileFor(t, "[nono.profile.linux]\naf_unix_mediation = \"off\"\n").Linux
	if off == nil || off.AfUnixMediation != "off" {
		t.Errorf("the [nono.profile] block must turn the block off, got %+v", off)
	}
}

// fnoxWorktree lays out a main checkout and a linked worktree beside it, each
// with the fnox files named, and returns both paths.
func fnoxWorktree(t *testing.T, inMain, inWorktree []string) (main, wt string) {
	t.Helper()
	root := t.TempDir()
	main = filepath.Join(root, "main")
	wt = filepath.Join(root, "wt")
	files := map[string]string{
		filepath.Join(main, ".git", "worktrees", "wt", "commondir"): "../..\n",
		filepath.Join(wt, ".git"):                                   "gitdir: " + filepath.Join(main, ".git", "worktrees", "wt") + "\n",
	}
	for _, n := range inMain {
		files[filepath.Join(main, n)] = ""
	}
	for _, n := range inWorktree {
		files[filepath.Join(wt, n)] = ""
	}
	for p, body := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return main, wt
}

// The local fnox file of the main checkout joins the committed fnox file of
// the worktree, and comes first so that it wins.
func TestFnoxTakesTheMissingLocalFileFromTheMainCheckout(t *testing.T) {
	main, wt := fnoxWorktree(t, []string{"fnox.toml", "fnox.local.toml"}, []string{"fnox.toml"})
	r, fromMain := newResolver(&config.Config{}, wt)
	want := []string{filepath.Join(main, "fnox.local.toml"), filepath.Join(wt, "fnox.toml")}
	if strings.Join(r.Imports, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", r.Imports, want)
	}
	if len(fromMain) != 1 || fromMain[0] != want[0] {
		t.Fatalf("got %v", fromMain)
	}
}

func TestFnoxSearchesItselfWhenTheWorktreeHasEveryFile(t *testing.T) {
	_, wt := fnoxWorktree(t, []string{"fnox.toml", "fnox.local.toml"}, []string{"fnox.toml", "fnox.local.toml"})
	r, fromMain := newResolver(&config.Config{}, wt)
	if len(r.Imports) != 0 || len(fromMain) != 0 {
		t.Fatalf("got %v, %v", r.Imports, fromMain)
	}
}

func TestFnoxConfigSettingTurnsTheFallbackOff(t *testing.T) {
	_, wt := fnoxWorktree(t, []string{"fnox.toml", "fnox.local.toml"}, nil)
	cfg := &config.Config{Fnox: config.Fnox{Config: "other.toml"}}
	r, fromMain := newResolver(cfg, wt)
	if len(r.Imports) != 0 || len(fromMain) != 0 {
		t.Fatalf("got %v, %v", r.Imports, fromMain)
	}
}

func TestFnoxTakesBothFilesFromTheMainCheckout(t *testing.T) {
	main, wt := fnoxWorktree(t, []string{"fnox.toml", "fnox.local.toml"}, nil)
	_, fromMain := newResolver(&config.Config{}, wt)
	want := []string{filepath.Join(main, "fnox.local.toml"), filepath.Join(main, "fnox.toml")}
	if strings.Join(fromMain, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", fromMain, want)
	}
}

func TestOnlyVerboseNamesAFileFromTheMainCheckout(t *testing.T) {
	files := []string{"/main/nn.toml", "/main/nn.local.toml"}
	for _, verbose := range []bool{false, true} {
		var out strings.Builder
		noteFromMain(options{stderr: &out, verbose: verbose}, files)
		got := strings.Count(out.String(), "\n")
		want := 0
		if verbose {
			want = 2
		}
		if got != want {
			t.Fatalf("verbose %v: got %d notes, want %d:\n%s", verbose, got, want, out.String())
		}
	}
}
