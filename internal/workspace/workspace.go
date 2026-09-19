// Package workspace owns the generated artifact directory under the project.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
)

// DirName is the artifact directory relative to the working directory. The
// profile refers to it as $WORKDIR/.nono/nn, which nono expands at launch.
const DirName = ".nono/nn"

// ProfileVar is the profile-side spelling of the artifact directory.
const ProfileVar = "$WORKDIR/.nono/nn"

type Workspace struct {
	Workdir string // absolute
	Dir     string // absolute artifact directory
}

func New(workdir string) (*Workspace, error) {
	abs, err := filepath.Abs(workdir)
	if err != nil {
		return nil, err
	}
	return &Workspace{Workdir: abs, Dir: filepath.Join(abs, DirName)}, nil
}

// Path resolves a path relative to the artifact directory.
func (w *Workspace) Path(rel string) string { return filepath.Join(w.Dir, rel) }

// ProfilePath is the file that nn passes to `nono run --profile`.
func (w *Workspace) ProfilePath() string { return w.Path("profile.json") }

// Write puts content at a path relative to the artifact directory, creating
// parents and replacing the file atomically so a reader never sees a partial
// credential file.
func (w *Workspace) Write(rel string, content []byte, mode os.FileMode) error {
	dst := w.Path(rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".nn-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// EnsureGitignore keeps the generated directory out of version control.
// Generated files can hold a kubeconfig and a cluster CA, so committing them
// is never wanted.
func (w *Workspace) EnsureGitignore() error {
	if err := os.MkdirAll(w.Dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(w.Dir, ".gitignore")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	body := "# Written by nn. Everything here is generated.\n*\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
