// Package discover locates nn's config file and resolves the paths inside it.
package discover

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// DirName is the conventional directory a config may live in, alongside
// profile.json.
const DirName = ".nono"

// ConfigNames are the accepted config filenames, in precedence order. Both
// spellings are accepted because guessing wrong is otherwise indistinguishable
// from having no config at all.
var ConfigNames = []string{"nn.yml", "nn.yaml"}

// ConfigName is the canonical name, used by `nn init` and in messages.
const ConfigName = "nn.yml"

// Dirs are the locations everything else is resolved against.
type Dirs struct {
	ConfigPath string // the nn.yml itself
	ConfigDir  string // directory holding it; profile and manifest paths anchor here
	Root       string // project root; other paths anchor here
}

// NotFoundError reports that no config was found, and where nn looked.
type NotFoundError struct {
	// Searched are the directories checked, in order.
	Searched []string
	// NonoDir is a .nono directory that exists but holds no config. Finding
	// one changes the advice completely, so it is worth saying.
	NonoDir string
}

func (e *NotFoundError) Error() string {
	if e.NonoDir != "" {
		return fmt.Sprintf("found %s but no %s in it\n"+
			"    create one with `nn init`, or point at an existing file with --config",
			e.NonoDir, strings.Join(ConfigNames, " or "))
	}
	return fmt.Sprintf("no %s found\n    looked in %s\n"+
		"    create one with `nn init`, or point at an existing file with --config",
		ConfigName, strings.Join(e.Searched, ", "))
}

// AmbiguousError reports two config files where only one may win. Picking
// silently would mean edits to the other file appear to do nothing.
type AmbiguousError struct {
	Dir   string
	Names []string
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%s contains both %s\n    remove one; nn will not guess",
		e.Dir, strings.Join(e.Names, " and "))
}

// Find looks for a config in startDir and, when startDir is inside a git
// repository, in the repository root as well. In each of those two directories
// it accepts a bare nn.yml or one under .nono/.
//
// It deliberately does not walk the whole way up. An unbounded search means a
// stray .nono/nn.yml in $HOME or any other ancestor silently claims an
// unrelated project; the git root is the one boundary above the cwd that
// reliably means "this project".
func Find(startDir string) (Dirs, error) {
	cwd, err := filepath.Abs(startDir)
	if err != nil {
		return Dirs{}, err
	}
	bases := []string{cwd}
	if root, ok := gitRoot(cwd); ok && root != cwd {
		bases = append(bases, root)
	}

	var searched []string
	var sawNonoDir string
	for _, base := range bases {
		// A bare config wins over one under .nono/ in the same directory.
		for _, dir := range []string{base, filepath.Join(base, DirName)} {
			searched = append(searched, dir)
			found, err := configIn(dir)
			if err != nil {
				return Dirs{}, err
			}
			if found != "" {
				return dirsFor(found), nil
			}
		}
		if sawNonoDir == "" {
			nonoDir := filepath.Join(base, DirName)
			if st, err := os.Stat(nonoDir); err == nil && st.IsDir() {
				sawNonoDir = nonoDir
			}
		}
	}
	return Dirs{}, &NotFoundError{Searched: searched, NonoDir: sawNonoDir}
}

// gitRoot walks up from dir looking for a .git entry, and reports the
// directory holding it.
//
// Existence is the test, not IsDir: a worktree or submodule checkout has .git
// as a *file* containing a `gitdir:` pointer, and both are equally the top of
// a working tree.
//
// Probing for .git rather than running `git rev-parse --show-toplevel` keeps
// this dependency- and subprocess-free. nn runs inside the sandboxes it
// builds, where git is not guaranteed to be on PATH or permitted to execute,
// and a discovery failure there would be reported as a missing config.
func gitRoot(dir string) (string, bool) {
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// configIn returns the config file in dir, or "" if there is none. Two
// spellings present at once is an error rather than a precedence rule: nn.yml
// beating nn.yaml across directories is a search order the user can reason
// about, but inside one directory it would just make edits to the loser appear
// to do nothing.
func configIn(dir string) (string, error) {
	var found []string
	for _, name := range ConfigNames {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			found = append(found, p)
		}
	}
	switch len(found) {
	case 0:
		return "", nil
	case 1:
		return found[0], nil
	default:
		names := make([]string, len(found))
		for i, p := range found {
			names[i] = filepath.Base(p)
		}
		return "", &AmbiguousError{Dir: dir, Names: names}
	}
}

// At builds Dirs for an explicit config path, as passed to --config or
// NN_CONFIG. The file's own directory takes the role of .nono.
func At(path string) (Dirs, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Dirs{}, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return Dirs{}, err
	}
	if st.IsDir() {
		return Dirs{}, errors.New(abs + " is a directory, want a config file")
	}
	return dirsFor(abs), nil
}

// dirsFor derives both anchors from the config's location.
//
// The config's own directory anchors profile and manifest paths, so
// `profile: profile.json` names the file sitting next to nn.yml in either
// layout. The project root is that same directory, except under .nono/, where
// it is the parent — otherwise `workdir: .` in a .nono/nn.yml would mean the
// .nono dir rather than the project.
func dirsFor(configPath string) Dirs {
	dir := filepath.Dir(configPath)
	root := dir
	if filepath.Base(dir) == DirName {
		root = filepath.Dir(dir)
	}
	return Dirs{ConfigPath: configPath, ConfigDir: dir, Root: root}
}

// profileExts are the suffixes that mark a profile reference as a file.
var profileExts = []string{".json", ".jsonc", ".yaml", ".yml"}

// IsPathLike reports whether s names a file rather than a nono profile name.
//
// A slash alone is not enough: "nolabs-ai/claude" is a registry pack name, and
// treating it as a relative path would silently break it. So a value is a path
// only if it is explicitly rooted, carries a profile file extension, or names
// a file that actually exists under configDir.
//
// This matches nono's own behaviour: `nono profile validate profile.json`
// reports "profile file not found" rather than "no such profile".
func IsPathLike(s, configDir string) bool {
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") ||
		strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") ||
		s == "." || s == ".." {
		return true
	}
	lower := strings.ToLower(s)
	for _, ext := range profileExts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	if configDir != "" && !filepath.IsAbs(s) {
		if _, err := os.Stat(filepath.Join(configDir, s)); err == nil {
			return true
		}
	}
	return false
}

// ResolveProfile turns a profile reference into an absolute path, or leaves it
// alone when it names a built-in or registry profile.
func ResolveProfile(s, configDir string) string {
	if !IsPathLike(s, configDir) {
		return s
	}
	return ResolvePath(s, configDir)
}

// ResolvePath expands a leading ~ and makes s absolute against anchor.
//
// $VAR is deliberately NOT expanded. nono profiles expand $HOME/$WORKDIR with
// their own semantics; a second expansion layer in nn would mean the same
// string means different things in two adjacent files.
func ResolvePath(s, anchor string) string {
	if s == "" {
		return s
	}
	p := expandTilde(s)
	if !filepath.IsAbs(p) {
		p = filepath.Join(anchor, p)
	}
	return filepath.Clean(p)
}

func expandTilde(s string) string {
	if s != "~" && !strings.HasPrefix(s, "~/") {
		return s
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return s
	}
	if s == "~" {
		return home
	}
	return filepath.Join(home, s[2:])
}

// binaryRE finds a profile's top-level "binary" key.
//
// A regex rather than a JSON parse on purpose: nono also accepts .jsonc, and
// this is a best-effort hint for herdr, not a correctness path. Anything it
// cannot read simply yields no hint.
var binaryRE = regexp.MustCompile(`"binary"\s*:\s*"([^"]+)"`)

// ProfileBinary reports the program a profile runs on its own, via its
// `binary` key. It is how nn can still name the agent when the config leaves
// `command:` empty and lets the profile decide.
func ProfileBinary(profilePath string) (string, bool) {
	data, err := os.ReadFile(profilePath)
	if err != nil {
		return "", false
	}
	if m := binaryRE.FindSubmatch(data); m != nil {
		return string(m[1]), true
	}
	return "", false
}
