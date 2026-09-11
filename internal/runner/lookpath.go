// Package runner hands the assembled invocation over to the OS.
package runner

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound is returned when a program is not on the given PATH.
var ErrNotFound = errors.New("executable file not found in $PATH")

// NotFoundError names which link in the exec chain could not be resolved.
type NotFoundError struct {
	Name string
	Path string
}

func (e *NotFoundError) Error() string { return e.Name + ": " + ErrNotFound.Error() }
func (e *NotFoundError) Unwrap() error { return ErrNotFound }

// LookPathIn resolves name against an explicit PATH value.
//
// exec.LookPath reads nn's own PATH, which is wrong here: when the config sets
// env.PATH, nn must resolve the chain against the PATH it is about to exec
// with. Otherwise nn finds one `nono` and hands control to an environment
// where a different one is first — an inconsistency that is close to
// undiagnosable from the outside.
func LookPathIn(name, pathVar string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) {
		if err := executable(name); err != nil {
			return "", &NotFoundError{Name: name, Path: pathVar}
		}
		return name, nil
	}
	for _, dir := range filepath.SplitList(pathVar) {
		if dir == "" {
			dir = "." // POSIX: an empty PATH element means the working directory
		}
		candidate := filepath.Join(dir, name)
		if err := executable(candidate); err == nil {
			if filepath.IsAbs(candidate) {
				return candidate, nil
			}
			if abs, err := filepath.Abs(candidate); err == nil {
				return abs, nil
			}
			return candidate, nil
		}
	}
	return "", &NotFoundError{Name: name, Path: pathVar}
}

func executable(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() || st.Mode()&0o111 == 0 {
		return fs.ErrPermission
	}
	return nil
}

// PathOf returns the PATH entry of a K=V environment slice.
func PathOf(env []string) string {
	for i := len(env) - 1; i >= 0; i-- { // last wins, as execve does
		if k, v, ok := strings.Cut(env[i], "="); ok && k == "PATH" {
			return v
		}
	}
	return ""
}
