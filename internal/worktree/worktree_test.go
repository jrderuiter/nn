package worktree

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// layout lays out a main checkout and a linked worktree beside it, the way
// git does: a .git file in the worktree and a commondir file in its private
// git directory.
func layout(t *testing.T) (main, wt string) {
	t.Helper()
	root := t.TempDir()
	main = filepath.Join(root, "main")
	wt = filepath.Join(root, "wt")
	write(t, filepath.Join(main, ".git", "HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(main, ".git", "worktrees", "wt", "commondir"), "../..\n")
	write(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(main, ".git", "worktrees", "wt")+"\n")
	return main, wt
}

func TestMainMapsTheSameDirectory(t *testing.T) {
	main, wt := layout(t)
	got, ok := Main(filepath.Join(wt, "a", "b"))
	if !ok || got != filepath.Join(main, "a", "b") {
		t.Fatalf("got %q, %v", got, ok)
	}
}

func TestMainIsFalseInTheMainCheckout(t *testing.T) {
	main, _ := layout(t)
	if got, ok := Main(main); ok {
		t.Fatalf("the main checkout has no main checkout, got %q", got)
	}
}

func TestMainReadsARelativeGitDir(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main")
	wt := filepath.Join(main, ".worktrees", "wt")
	write(t, filepath.Join(main, ".git", "worktrees", "wt", "commondir"), "../..\n")
	write(t, filepath.Join(wt, ".git"), "gitdir: ../../.git/worktrees/wt\n")
	got, ok := Main(wt)
	if !ok || got != main {
		t.Fatalf("got %q, %v", got, ok)
	}
}

// A submodule also has a .git file, but its files belong to it alone.
func TestMainIsFalseInASubmodule(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "modules", "sub", "HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(root, "sub", ".git"), "gitdir: ../.git/modules/sub\n")
	if got, ok := Main(filepath.Join(root, "sub")); ok {
		t.Fatalf("got %q", got)
	}
}

func TestLookupPrefersTheWorktree(t *testing.T) {
	main, wt := layout(t)
	write(t, filepath.Join(main, "nn.toml"), "")
	write(t, filepath.Join(wt, "nn.toml"), "")
	p, fromMain := Lookup(wt, "nn.toml")
	if p != filepath.Join(wt, "nn.toml") || fromMain {
		t.Fatalf("got %q, %v", p, fromMain)
	}
}

func TestLookupFallsBackToTheMainCheckout(t *testing.T) {
	main, wt := layout(t)
	write(t, filepath.Join(main, "nn.toml"), "")
	p, fromMain := Lookup(filepath.Join(wt, "a"), "nn.toml")
	if p != filepath.Join(main, "nn.toml") || !fromMain {
		t.Fatalf("got %q, %v", p, fromMain)
	}
}

func TestLookupFindsNothing(t *testing.T) {
	_, wt := layout(t)
	if p, _ := Lookup(wt, "nn.toml"); p != "" {
		t.Fatalf("got %q", p)
	}
}

func TestBesideDoesNotWalkUp(t *testing.T) {
	main, wt := layout(t)
	write(t, filepath.Join(main, "nn.local.toml"), "")
	if p, _ := Beside(filepath.Join(wt, "a"), "nn.local.toml"); p != "" {
		t.Fatalf("got %q", p)
	}
	p, fromMain := Beside(wt, "nn.local.toml")
	if p != filepath.Join(main, "nn.local.toml") || !fromMain {
		t.Fatalf("got %q, %v", p, fromMain)
	}
}
