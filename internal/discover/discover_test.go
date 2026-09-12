package discover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resolved compares through EvalSymlinks: t.TempDir can hand back a symlinked
// path (/var -> /private/var on macOS).
func resolved(t *testing.T, got, want, what string) {
	t.Helper()
	g, _ := filepath.EvalSymlinks(got)
	w, _ := filepath.EvalSymlinks(want)
	if g != w {
		t.Errorf("%s = %s, want %s", what, g, w)
	}
}

// gitProject builds a project with a .git marker at its top and returns the
// top and a deep subdirectory to search from.
func gitProject(t *testing.T) (root, deep string) {
	t.Helper()
	root = t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep = filepath.Join(root, "cmd", "server", "internal")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, deep
}

func writeConfig(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("command: [echo]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A bare nn.yml beside the config is the new second supported layout. Its own
// directory is both the config anchor and the project root.
func TestFindsBareConfigInCwd(t *testing.T) {
	root := t.TempDir()
	cfg := writeConfig(t, root, ConfigName)

	dirs, err := Find(root)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	resolved(t, dirs.ConfigPath, cfg, "ConfigPath")
	resolved(t, dirs.ConfigDir, root, "ConfigDir")
	resolved(t, dirs.Root, root, "Root")
}

// With a bare config the project root must be the config's own directory. The
// old rule, the grandparent, would put the workdir anchor outside the project.
func TestRootAnchorForBareConfig(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, ConfigName)

	dirs, err := Find(root)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if dirs.Root == filepath.Dir(dirs.ConfigDir) {
		t.Errorf("Root = %s, must not be the config dir's parent", dirs.Root)
	}
	resolved(t, dirs.Root, root, "Root")
}

// Under .nono/ the root is still the parent, so `workdir: .` means the project
// rather than the .nono dir.
func TestRootAnchorUnderNonoDir(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, filepath.Join(root, DirName), ConfigName)

	dirs, err := Find(root)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	resolved(t, dirs.ConfigDir, filepath.Join(root, DirName), "ConfigDir")
	resolved(t, dirs.Root, root, "Root")
}

// A bare config wins over one under .nono/ in the same directory.
func TestBareConfigBeatsNonoDir(t *testing.T) {
	root := t.TempDir()
	bare := writeConfig(t, root, ConfigName)
	writeConfig(t, filepath.Join(root, DirName), ConfigName)

	dirs, err := Find(root)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	resolved(t, dirs.ConfigPath, bare, "ConfigPath")
}

// Across directories .yml wins, and that is a precedence rule rather than the
// conflict two spellings in one directory would be.
func TestYmlBeatsYamlAcrossDirs(t *testing.T) {
	root := t.TempDir()
	bare := writeConfig(t, root, "nn.yml")
	writeConfig(t, filepath.Join(root, DirName), "nn.yaml")

	dirs, err := Find(root)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	resolved(t, dirs.ConfigPath, bare, "ConfigPath")
}

// The git root is what makes a config reachable from a subdirectory now that
// there is no unbounded walk. All four layouts must be found there.
func TestFindsConfigAtGitRoot(t *testing.T) {
	for _, layout := range []struct{ dir, name string }{
		{"", "nn.yml"},
		{"", "nn.yaml"},
		{DirName, "nn.yml"},
		{DirName, "nn.yaml"},
	} {
		t.Run(filepath.Join(layout.dir, layout.name), func(t *testing.T) {
			root, deep := gitProject(t)
			cfg := writeConfig(t, filepath.Join(root, layout.dir), layout.name)

			dirs, err := Find(deep)
			if err != nil {
				t.Fatalf("Find: %v", err)
			}
			resolved(t, dirs.ConfigPath, cfg, "ConfigPath")
			resolved(t, dirs.Root, root, "Root")
		})
	}
}

// A worktree or submodule checkout has .git as a file holding a gitdir:
// pointer. It is equally the top of a working tree.
func TestGitRootFromAGitFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /elsewhere/worktrees/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "cmd", "server")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := writeConfig(t, root, ConfigName)

	dirs, err := Find(deep)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	resolved(t, dirs.ConfigPath, cfg, "ConfigPath")
}

// The nearer config wins: a per-directory config overrides the project's.
func TestCwdBeatsGitRoot(t *testing.T) {
	root, deep := gitProject(t)
	writeConfig(t, root, ConfigName)
	near := writeConfig(t, filepath.Join(deep, DirName), ConfigName)

	dirs, err := Find(deep)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	resolved(t, dirs.ConfigPath, near, "ConfigPath")
}

// The unbounded walk is gone. Without a git root there is nothing above the
// cwd to search, so an ancestor's config is deliberately not found.
func TestNoWalkUpWithoutGit(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, filepath.Join(root, DirName), ConfigName)
	deep := filepath.Join(root, "cmd", "server")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Find(deep)
	var nf *NotFoundError
	if err == nil || !asNotFound(err, &nf) {
		t.Fatalf("want *NotFoundError, got %T: %v", err, err)
	}
}

// The search also stops going up at the git root: a config above it belongs to
// an outer project.
func TestDoesNotSearchAboveGitRoot(t *testing.T) {
	outer := t.TempDir()
	writeConfig(t, filepath.Join(outer, DirName), ConfigName)
	inner := filepath.Join(outer, "vendor", "dep")
	if err := os.MkdirAll(filepath.Join(inner, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Find(inner)
	var nf *NotFoundError
	if err == nil || !asNotFound(err, &nf) {
		t.Fatalf("want *NotFoundError, got %T: %v", err, err)
	}
}

func TestFindNotFound(t *testing.T) {
	// A temp dir with no .nono anywhere above it short of /.
	_, err := Find(t.TempDir())
	var nf *NotFoundError
	if err == nil {
		t.Fatal("want NotFoundError, got nil")
	}
	if !asNotFound(err, &nf) {
		t.Fatalf("want *NotFoundError, got %T: %v", err, err)
	}
}

func asNotFound(err error, target **NotFoundError) bool {
	e, ok := err.(*NotFoundError)
	if ok {
		*target = e
	}
	return ok
}

func TestIsPathLike(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		in   string
		want bool
		why  string
	}{
		{"profile.json", true, "profile extension"},
		{"custom.jsonc", true, "jsonc extension"},
		{"./p", true, "explicitly rooted"},
		{"../p", true, "explicitly rooted"},
		{"/abs/p", true, "absolute"},
		{"~/p", true, "home-relative"},
		{"existing", true, "file exists under nonoDir"},
		{"nolabs-ai/claude", false, "registry pack name, not a path"},
		{"claude-code", false, "bare profile name"},
		{"go-dev", false, "bare profile name"},
		{"", false, "empty"},
	}
	for _, c := range cases {
		if got := IsPathLike(c.in, dir); got != c.want {
			t.Errorf("IsPathLike(%q) = %v, want %v (%s)", c.in, got, c.want, c.why)
		}
	}
}

func TestResolveProfile(t *testing.T) {
	nonoDir := "/proj/.nono"
	if got := ResolveProfile("profile.json", nonoDir); got != "/proj/.nono/profile.json" {
		t.Errorf("relative profile: got %s", got)
	}
	if got := ResolveProfile("nolabs-ai/claude", nonoDir); got != "nolabs-ai/claude" {
		t.Errorf("registry name must pass through: got %s", got)
	}
	if got := ResolveProfile("/etc/p.json", nonoDir); got != "/etc/p.json" {
		t.Errorf("absolute passthrough: got %s", got)
	}
}

func TestResolvePathAnchors(t *testing.T) {
	// workdir "." must mean the project root, not the .nono dir.
	if got := ResolvePath(".", "/proj"); got != "/proj" {
		t.Errorf(`ResolvePath(".", "/proj") = %s, want /proj`, got)
	}
	if got := ResolvePath("sub/dir", "/proj"); got != "/proj/sub/dir" {
		t.Errorf("relative: got %s", got)
	}
	if got := ResolvePath("/abs", "/proj"); got != "/abs" {
		t.Errorf("absolute passthrough: got %s", got)
	}
	home, _ := os.UserHomeDir()
	if got := ResolvePath("~/x", "/proj"); got != filepath.Join(home, "x") {
		t.Errorf("tilde: got %s", got)
	}
	// $VAR is nono's job inside profile.json, not nn's.
	if got := ResolvePath("$HOME/x", "/proj"); got != "/proj/$HOME/x" {
		t.Errorf("$VAR must not expand: got %s", got)
	}
}

// Both spellings are accepted: guessing wrong would otherwise be
// indistinguishable from having no config at all.
func TestFindAcceptsBothSpellings(t *testing.T) {
	for _, name := range ConfigNames {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			nono := filepath.Join(root, DirName)
			if err := os.MkdirAll(nono, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nono, name), []byte("command: [x]\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			dirs, err := Find(root)
			if err != nil {
				t.Fatalf("Find: %v", err)
			}
			if filepath.Base(dirs.ConfigPath) != name {
				t.Errorf("ConfigPath = %s, want %s", dirs.ConfigPath, name)
			}
		})
	}
}

// Silently preferring one would make edits to the other appear to do nothing.
func TestFindRejectsBothSpellingsAtOnce(t *testing.T) {
	root := t.TempDir()
	nono := filepath.Join(root, DirName)
	if err := os.MkdirAll(nono, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range ConfigNames {
		if err := os.WriteFile(filepath.Join(nono, name), []byte("command: [x]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := Find(root)
	var amb *AmbiguousError
	if err == nil || !errorsAs(err, &amb) {
		t.Fatalf("want *AmbiguousError, got %T: %v", err, err)
	}
}

// Finding a .nono with no config in it calls for different advice than
// finding no .nono at all.
func TestNotFoundMentionsAnEmptyNonoDir(t *testing.T) {
	root := t.TempDir()
	nono := filepath.Join(root, DirName)
	if err := os.MkdirAll(nono, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nono, "profile.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Find(root)
	if err == nil {
		t.Fatal("want an error")
	}
	var nf *NotFoundError
	if !asNotFound(err, &nf) {
		t.Fatalf("want *NotFoundError, got %T", err)
	}
	if nf.NonoDir == "" {
		t.Error("NonoDir should name the directory that was found")
	}
	if !strings.Contains(err.Error(), DirName) {
		t.Errorf("message should name the directory:\n%v", err)
	}
}

// The message has to list where nn actually looked; there is no walk to "/"
// to describe any more.
func TestNotFoundListsWhereItLooked(t *testing.T) {
	root, deep := gitProject(t)

	_, err := Find(deep)
	var nf *NotFoundError
	if err == nil || !asNotFound(err, &nf) {
		t.Fatalf("want *NotFoundError, got %T: %v", err, err)
	}
	for _, want := range []string{deep, filepath.Join(deep, DirName), root, filepath.Join(root, DirName)} {
		if !containsPath(nf.Searched, want) {
			t.Errorf("Searched %v should include %s", nf.Searched, want)
		}
	}
	if !strings.Contains(err.Error(), deep) {
		t.Errorf("message should name the directories searched:\n%v", err)
	}
}

func containsPath(haystack []string, want string) bool {
	for _, h := range haystack {
		if h == want {
			return true
		}
	}
	return false
}

// The same conflict applies to a bare config, where there is no .nono dir to
// blame it on.
func TestFindRejectsBothSpellingsBare(t *testing.T) {
	root := t.TempDir()
	for _, name := range ConfigNames {
		writeConfig(t, root, name)
	}
	_, err := Find(root)
	var amb *AmbiguousError
	if err == nil || !errorsAs(err, &amb) {
		t.Fatalf("want *AmbiguousError, got %T: %v", err, err)
	}
}

func errorsAs(err error, target **AmbiguousError) bool {
	e, ok := err.(*AmbiguousError)
	if ok {
		*target = e
	}
	return ok
}
