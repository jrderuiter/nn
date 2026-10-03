package nono

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"syscall"
)

// Binary is the nono executable name.
var Binary = "nono"

// Marshal renders a profile as the JSON that nn writes to disk.
func Marshal(p *Profile) ([]byte, error) {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// LookPath makes sure that the nono binary is reachable.
func LookPath() (string, error) {
	path, err := exec.LookPath(Binary)
	if err != nil {
		return "", fmt.Errorf("nono binary %q not found on PATH: %w", Binary, err)
	}
	return path, nil
}

type validation struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

// Validate runs `nono profile validate` so a generated profile fails here,
// with nono's own message, rather than halfway into a launch.
func Validate(path string) error {
	out, err := exec.Command(Binary, "profile", "validate", path, "--json").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(out) == 0 {
			return fmt.Errorf("nono profile validate failed: %s", string(ee.Stderr))
		}
		if len(out) == 0 {
			return fmt.Errorf("nono profile validate failed: %w", err)
		}
	}
	var v validation
	if err := json.Unmarshal(out, &v); err != nil {
		return fmt.Errorf("cannot read the output of nono profile validate: %w", err)
	}
	if !v.Valid {
		return fmt.Errorf("the generated profile is not valid: %v", v.Errors)
	}
	return nil
}

// RunArgs builds the argv for `nono run`.
type RunArgs struct {
	ProfilePath string
	Workdir     string
	Extra       []string
	Command     []string
	// Banner shows nono's capability table and status lines. It is off by
	// default: nn already knows what it generated, and the table is noise in
	// front of an agent.
	Banner bool
	// Diagnostics shows nono's report of the paths it blocked during the run.
	// It is useful when something inside the sandbox fails for no clear
	// reason, and noise otherwise.
	Diagnostics bool
}

func (a RunArgs) Build() []string {
	args := []string{"run"}
	if !a.Banner {
		args = append(args, "-s")
	}
	if !a.Diagnostics {
		args = append(args, "--no-diagnostics")
	}
	if a.Workdir != "" {
		args = append(args, "--workdir", a.Workdir)
	}
	// The generated profile states the working directory access level, so the
	// prompt has nothing to add. Without this flag a non-interactive run fails
	// with "CWD access requires --allow-cwd".
	args = append(args, "--allow-cwd")
	args = append(args, "--profile", a.ProfilePath)
	args = append(args, a.Extra...)
	args = append(args, "--")
	return append(args, a.Command...)
}

// Env builds the environment for the nono process.
//
// WORKDIR is set because a few profile fields, such as a credential route's
// tls_ca, resolve that name from the environment rather than expanding it
// themselves. Setting it here keeps those paths relative, so a generated
// profile stays portable between machines.
//
// HERDR_AGENT names the agent that the sandboxed command runs, for tools on the
// host that read it. It is left out when the command is not a known agent, and
// it is not in the generated allow_vars list, so it reaches nono and stops
// there.
func Env(workdir, agent string, extra []string) []string {
	vars := map[string]string{}
	if workdir != "" {
		vars["WORKDIR"] = workdir
	}
	if agent != "" {
		vars["HERDR_AGENT"] = agent
	}
	for _, kv := range extra {
		name, value, ok := strings.Cut(kv, "=")
		if ok {
			vars[name] = value
		}
	}
	if len(vars) == 0 {
		return os.Environ()
	}
	out := make([]string, 0, len(os.Environ())+len(vars))
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if _, replaced := vars[name]; replaced {
			continue
		}
		out = append(out, kv)
	}
	// Sorted, so the environment nn hands over does not depend on map order.
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, name+"="+vars[name])
	}
	return out
}

// Exec replaces the current process with nono. Handing the terminal straight
// over gives correct signal delivery and the real exit code, and leaves no nn
// process in the tree.
//
// Inside a herdr pane, nn starts nono as a child instead, and exits with its
// status. See InHerdr for why.
func Exec(args []string, env []string) error {
	code, err := launch(args, env)
	if err != nil {
		return err
	}
	os.Exit(code)
	return nil
}

// launch runs nono and returns its exit status. With syscall.Exec it returns
// only on failure.
func launch(args []string, env []string) (int, error) {
	path, err := LookPath()
	if err != nil {
		return 0, err
	}
	if runtime.GOOS == "windows" || InHerdr() {
		return runChild(path, args, env)
	}
	return 0, syscall.Exec(path, append([]string{Binary}, args...), env)
}

// InHerdr says whether nn runs in a herdr pane.
//
// herdr finds the agent of a pane by reading HERDR_AGENT from the environ
// file of the foreground process group leader. nono's supervisor makes itself
// undumpable, and Linux then hides that file from other processes. So in a
// herdr pane nn stays in front of nono as the group leader, and carries
// HERDR_AGENT in its own environment.
func InHerdr() bool {
	return os.Getenv("HERDR_ENV") == "1"
}

// ExecWithAgent makes sure that the environment nn started with names the
// agent, because herdr reads the environ file, and os.Setenv does not change
// it. When the value is wrong, nn replaces itself with a copy that has the
// right one, and the copy finds nothing left to change. Outside herdr it does
// nothing.
//
// Call it before secrets are resolved, so a backend that asks for a touch asks
// once, and no secret lands in the environment of nn.
func ExecWithAgent(agent string) error {
	if runtime.GOOS == "windows" || !InHerdr() {
		return nil
	}
	env, changed := withAgent(os.Environ(), agent)
	if !changed {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find the nn executable: %w", err)
	}
	return syscall.Exec(exe, os.Args, env)
}

// withAgent sets HERDR_AGENT in environ to agent, or removes it when agent is
// empty. It reports whether that changed anything.
func withAgent(environ []string, agent string) ([]string, bool) {
	out := make([]string, 0, len(environ)+1)
	found := false
	changed := false
	for _, kv := range environ {
		name, value, _ := strings.Cut(kv, "=")
		if name != "HERDR_AGENT" {
			out = append(out, kv)
			continue
		}
		if found || agent == "" || value != agent {
			changed = true
			continue
		}
		found = true
		out = append(out, kv)
	}
	if agent != "" && !found {
		out = append(out, "HERDR_AGENT="+agent)
		changed = true
	}
	return out, changed
}

// runChild runs nono as a child process and returns its exit status, with
// 128 plus the signal number when a signal killed it.
//
// nono shares the process group of the terminal, so SIGINT and SIGQUIT from
// the terminal reach it directly. nn catches them and drops them, so that it
// outlives nono. signal.Ignore would not do: an ignored signal stays ignored
// in the child after exec. SIGTERM and SIGHUP come from outside the group, so
// nn passes them on.
func runChild(path string, args []string, env []string) (int, error) {
	cmd := exec.Command(path, args...)
	cmd.Args[0] = Binary
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)

	if err := cmd.Start(); err != nil {
		return 0, err
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case s := <-sigs:
				if s == syscall.SIGTERM || s == syscall.SIGHUP {
					_ = cmd.Process.Signal(s)
				}
			case <-done:
				return
			}
		}
	}()

	err := cmd.Wait()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		return 0, err
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal()), nil
	}
	return cmd.ProcessState.ExitCode(), nil
}
