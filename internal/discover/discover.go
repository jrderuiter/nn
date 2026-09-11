// Package discover locates .nono/nn.yml and resolves the paths inside it.
package discover

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ConfigName is the file nn looks for, inside a .nono directory.
const (
	DirName    = ".nono"
	ConfigName = "nn.yml"
)

// Dirs are the locations everything else is resolved against.
type Dirs struct {
	ConfigPath string // the nn.yml itself
	NonoDir    string // directory holding nn.yml; profile paths anchor here
	Root       string // project root, NonoDir's parent; other paths anchor here
}

// NotFoundError reports that no config was found, and where nn looked.
type NotFoundError struct {
	From string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("no %s/%s found\n    searched from %s up to %s\n"+
		"    create one with `nn init`, or point at an existing file with --config",
		DirName, ConfigName, e.From, string(filepath.Separator))
}

// Find walks up from startDir looking for .nono/nn.yml. It walks to the
// filesystem root: a .nono/nn.yml in $HOME is a legitimate personal default,
// so there is no reason to stop short of it.
func Find(startDir string) (Dirs, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return Dirs{}, err
	}
	from := dir
	for {
		candidate := filepath.Join(dir, DirName, ConfigName)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return dirsFor(candidate), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Dirs{}, &NotFoundError{From: from}
		}
		dir = parent
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

func dirsFor(configPath string) Dirs {
	nonoDir := filepath.Dir(configPath)
	return Dirs{
		ConfigPath: configPath,
		NonoDir:    nonoDir,
		Root:       filepath.Dir(nonoDir),
	}
}

// profileExts are the suffixes that mark a profile reference as a file.
var profileExts = []string{".json", ".jsonc", ".yaml", ".yml"}

// IsPathLike reports whether s names a file rather than a nono profile name.
//
// A slash alone is not enough: "nolabs-ai/claude" is a registry pack name, and
// treating it as a relative path would silently break it. So a value is a path
// only if it is explicitly rooted, carries a profile file extension, or names
// a file that actually exists under nonoDir.
//
// This matches nono's own behaviour: `nono profile validate profile.json`
// reports "profile file not found" rather than "no such profile".
func IsPathLike(s, nonoDir string) bool {
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
	if nonoDir != "" && !filepath.IsAbs(s) {
		if _, err := os.Stat(filepath.Join(nonoDir, s)); err == nil {
			return true
		}
	}
	return false
}

// ResolveProfile turns a profile reference into an absolute path, or leaves it
// alone when it names a built-in or registry profile.
func ResolveProfile(s, nonoDir string) string {
	if !IsPathLike(s, nonoDir) {
		return s
	}
	return ResolvePath(s, nonoDir)
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
