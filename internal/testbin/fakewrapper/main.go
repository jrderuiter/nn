// Command fakewrapper stands in for fnox/mise/direnv: it marks the
// environment and execs whatever follows the first --, so tests can prove the
// wrapper chain survives more than one exec hop.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func main() {
	argv := os.Args[1:]
	sep := -1
	for i, a := range argv {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 0 || sep+1 >= len(argv) {
		fmt.Fprintln(os.Stderr, "fakewrapper: nothing to exec")
		os.Exit(2)
	}
	rest := argv[sep+1:]

	env := append(os.Environ(), "FAKE_WRAPPER_RAN=1")
	path, err := exec.LookPath(rest[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakewrapper: %v\n", err)
		os.Exit(127)
	}
	if err := syscall.Exec(path, rest, env); err != nil {
		fmt.Fprintf(os.Stderr, "fakewrapper: %v\n", err)
		os.Exit(1)
	}
}
