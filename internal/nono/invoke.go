package nono

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
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
func Env(workdir, agent string) []string {
	vars := map[string]string{}
	if workdir != "" {
		vars["WORKDIR"] = workdir
	}
	if agent != "" {
		vars["HERDR_AGENT"] = agent
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
	for _, name := range []string{"HERDR_AGENT", "WORKDIR"} {
		if v, ok := vars[name]; ok {
			out = append(out, name+"="+v)
		}
	}
	return out
}

// Exec replaces the current process with nono. Handing the terminal straight
// over gives correct signal delivery and the real exit code, and leaves no nn
// process in the tree.
func Exec(args []string, env []string) error {
	path, err := LookPath()
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		cmd := exec.Command(path, args...)
		cmd.Env = env
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				os.Exit(ee.ExitCode())
			}
			return err
		}
		return nil
	}
	return syscall.Exec(path, append([]string{Binary}, args...), env)
}
