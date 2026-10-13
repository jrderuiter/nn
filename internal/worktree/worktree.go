// Package worktree finds configuration files for a directory, and falls back to
// the main checkout when the directory is in a linked git worktree.
//
// A new worktree holds only committed files. The files that a team does not
// commit, such as nn.local.toml, stay behind in the main checkout. Without a
// fallback, every new worktree would need its own copy of each one.
//
// Every lookup prefers the worktree. The main checkout counts only for a file
// that the worktree does not have.
package worktree

import (
	"os"
	"path/filepath"
	"strings"
)

// Find walks up from dir and returns the first file it finds with one of the
// names. In one directory, the names are tried in order. The walk stops at the
// top of the checkout, the first directory with a .git entry, so a nested
// repository never picks up the files of the repository around it. Find returns
// an empty string when there is no such file.
func Find(dir string, names ...string) string {
	cur, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if p := first(cur, names); p != "" {
			return p
		}
		if exists(filepath.Join(cur, ".git")) {
			return ""
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// Lookup is Find, with a fallback. When the worktree has no such file, Lookup
// repeats the search from the same place in the main checkout. fromMain says
// that the file came from the main checkout.
func Lookup(dir string, names ...string) (path string, fromMain bool) {
	if p := Find(dir, names...); p != "" {
		return p, false
	}
	if alt, ok := Main(dir); ok {
		if p := Find(alt, names...); p != "" {
			return p, true
		}
	}
	return "", false
}

// Beside returns the first file with one of the names in dir itself. When dir
// has none, Beside looks in the same directory of the main checkout. It does
// not walk up, because a local file belongs to the directory of the project
// file that it changes.
func Beside(dir string, names ...string) (path string, fromMain bool) {
	if p := first(dir, names); p != "" {
		return p, false
	}
	if alt, ok := Main(dir); ok {
		if p := first(alt, names); p != "" {
			return p, true
		}
	}
	return "", false
}

// Main returns the directory in the main checkout that matches dir. ok is
// false when dir is not in a linked worktree.
//
// Main reads the files that git writes for a worktree instead of running git.
// The .git entry of a linked worktree is a file that names its private git
// directory, and that directory holds a commondir file that names the shared
// one. A submodule also has a .git file, but its git directory has no
// commondir, so Main does not take it for a worktree.
func Main(dir string) (string, bool) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	top := checkoutTop(dir)
	if top == "" {
		return "", false
	}
	b, err := os.ReadFile(filepath.Join(top, ".git"))
	if err != nil {
		// A .git directory, not a file: this is the main checkout.
		return "", false
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return "", false
	}
	gitDir = resolve(top, strings.TrimSpace(gitDir))
	c, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return "", false
	}
	common := resolve(gitDir, strings.TrimSpace(string(c)))
	// A bare repository has no main checkout to fall back to.
	if filepath.Base(common) != ".git" {
		return "", false
	}
	rel, err := filepath.Rel(top, dir)
	if err != nil {
		return "", false
	}
	return filepath.Join(filepath.Dir(common), rel), true
}

// checkoutTop returns the nearest directory, dir included, with a .git entry.
func checkoutTop(dir string) string {
	for {
		if exists(filepath.Join(dir, ".git")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func first(dir string, names []string) string {
	for _, n := range names {
		p := filepath.Join(dir, n)
		if exists(p) {
			return p
		}
	}
	return ""
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func resolve(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}
