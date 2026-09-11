//go:build unix

package runner

import "syscall"

// Exec replaces this process with argv, resolved against env's own PATH.
//
// nn has no work left once argv is assembled, and stepping out of the way is
// strictly better than supervising:
//
//   - Signals. `nono run` is itself a supervisor and the child is usually a
//     full-screen TUI. As a parent, nn would have to forward SIGINT, TERM,
//     QUIT, HUP, WINCH and TSTP — and Ctrl-C goes to the whole foreground
//     process group, so nn would receive it alongside nono and could race to
//     exit first, orphaning the sandbox.
//   - The terminal. After exec, nono inherits nn's controlling terminal and
//     stdio descriptors directly: no PTY to allocate, no lost SIGWINCH, and
//     nono's own alt-screen startup detection keeps working.
//   - Exit codes. There is no ExitError to unwrap and no 128+signal to
//     reconstruct; nn's exit status simply is the child's.
//
// It also matches what every other link in the chain does: fnox exec, mise
// exec, direnv exec and nono wrap all exec and disappear.
func Exec(argv, env []string) error {
	path, err := LookPathIn(argv[0], PathOf(env))
	if err != nil {
		return err
	}
	return syscall.Exec(path, argv, env) // does not return on success
}
