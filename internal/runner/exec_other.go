//go:build !unix

package runner

import (
	"os"
	"os/exec"
	"os/signal"
)

// Exec runs argv and propagates its exit code.
//
// syscall.Exec is Unix-only. This fallback exists so the tree builds and vets
// cleanly on other platforms; nono itself targets macOS and Linux.
func Exec(argv, env []string) error {
	path, err := LookPathIn(argv[0], PathOf(env))
	if err != nil {
		return err
	}
	cmd := exec.Command(path, argv[1:]...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs)
	defer signal.Stop(sigs)

	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		for s := range sigs {
			_ = cmd.Process.Signal(s)
		}
	}()
	if err := cmd.Wait(); err != nil {
		var ee *exec.ExitError
		if errorsAs(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	os.Exit(0)
	return nil
}
