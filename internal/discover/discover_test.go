package discover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindWalksUp(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "cmd", "server", "internal")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	nono := filepath.Join(root, DirName)
	if err := os.MkdirAll(nono, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(nono, ConfigName)
	if err := os.WriteFile(cfg, []byte("command: [echo]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirs, err := Find(deep)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	// t.TempDir can hand back a symlinked path (/var -> /private/var on
	// macOS), so compare resolved forms.
	wantCfg, _ := filepath.EvalSymlinks(cfg)
	gotCfg, _ := filepath.EvalSymlinks(dirs.ConfigPath)
	if gotCfg != wantCfg {
		t.Errorf("ConfigPath = %s, want %s", gotCfg, wantCfg)
	}
	wantRoot, _ := filepath.EvalSymlinks(root)
	gotRoot, _ := filepath.EvalSymlinks(dirs.Root)
	if gotRoot != wantRoot {
		t.Errorf("Root = %s, want %s", gotRoot, wantRoot)
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

func errorsAs(err error, target **AmbiguousError) bool {
	e, ok := err.(*AmbiguousError)
	if ok {
		*target = e
	}
	return ok
}
