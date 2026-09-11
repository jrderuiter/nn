package config

import (
	"fmt"
	"sort"
	"strings"
)

// Error is a config problem phrased for the terminal: what failed, and what to
// do about it. Hint is printed indented under Msg.
type Error struct {
	Msg  string
	Hint string
}

func (e *Error) Error() string {
	if e.Hint == "" {
		return e.Msg
	}
	return e.Msg + "\n    " + strings.ReplaceAll(e.Hint, "\n", "\n    ")
}

func errf(hint, format string, a ...any) *Error {
	return &Error{Msg: fmt.Sprintf(format, a...), Hint: hint}
}

// exclusivePairs are flags where nono offers both the positive and the
// negative spelling. Setting both is always a mistake, and picking a winner
// silently would hide it.
var exclusivePairs = [][2]string{
	{"rollback", "no_rollback"},
	{"audit_integrity", "no_audit_integrity"},
}

// Validate checks the whole file, including blocks for modes that are not
// being invoked. A typo in the `wrap:` block should not wait for CI to surface.
func Validate(f *File) error {
	if f.Mode != nil {
		return errf("use the `nn run` / `nn shell` / `nn wrap` subcommands instead",
			"`mode` is not a config key")
	}

	// Strict half: a key inside a mode block asserts it applies to that mode.
	for _, m := range []Mode{ModeRun, ModeShell, ModeWrap} {
		b := f.Block(m)
		if b == nil {
			continue
		}
		if err := validateBlockModes(b, m); err != nil {
			return err
		}
		if err := validateSection(b, m, "`"+m.String()+":` block"); err != nil {
			return err
		}
	}
	return validateSection(&f.Section, 0, "top level")
}

func validateBlockModes(b *Section, m Mode) error {
	var bad []string
	for _, spec := range Specs {
		if spec.Modes&m == 0 && IsKeySet(b, spec.Key) {
			bad = append(bad, spec.Key)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	var lines []string
	for _, key := range bad {
		spec := SpecByKey[key]
		lines = append(lines, fmt.Sprintf("%s (%s) is available in: %s",
			key, spec.Flag, strings.Join(spec.Modes.Names(), ", ")))
	}
	return errf(strings.Join(lines, "\n")+
		"\nmove these to the top level to have them apply only where supported",
		"the `%s:` block sets %d key(s) that `nono %s` does not accept",
		m, len(bad), m)
}

// validateSection checks rules that hold wherever a key appears. mode is 0 for
// the top-level section, where mode-specific rules do not apply.
func validateSection(s *Section, mode Mode, where string) error {
	for _, pair := range exclusivePairs {
		if IsKeySet(s, pair[0]) && IsKeySet(s, pair[1]) {
			return errf("nono has both flags; set only the one you mean",
				"%s sets both `%s` and `%s`", where, pair[0], pair[1])
		}
	}

	if IsKeySet(s, "config") {
		var conflicts []string
		for _, spec := range Specs {
			if spec.Sandbox && IsKeySet(s, spec.Key) {
				conflicts = append(conflicts, spec.Key)
			}
		}
		if len(conflicts) > 0 {
			sort.Strings(conflicts)
			return errf("a capability manifest is already fully resolved; remove the others\nconflicting keys: "+
				strings.Join(conflicts, ", "),
				"%s: `config` (nono's -c manifest) is mutually exclusive with sandbox keys", where)
		}
	}

	for i, w := range s.Wrappers {
		if len(w) == 0 {
			return errf("each wrapper is a command prefix, e.g. [fnox, exec, --]",
				"%s: wrappers[%d] is empty", where, i)
		}
		if w[0] == "" {
			return errf("each wrapper is a command prefix, e.g. [fnox, exec, --]",
				"%s: wrappers[%d] starts with an empty string", where, i)
		}
	}

	for _, k := range s.EnvUnset {
		if _, ok := s.Env[k]; ok {
			return errf("setting and removing the same variable is a mistake; keep one",
				"%s: %q appears in both `env` and `env_unset`", where, k)
		}
	}
	for k := range s.Env {
		if err := ValidEnvKey(k); err != nil {
			return errf("", "%s: env key %q: %v", where, k, err)
		}
	}
	for _, k := range s.EnvUnset {
		if err := ValidEnvKey(k); err != nil {
			return errf("", "%s: env_unset entry %q: %v", where, k, err)
		}
	}

	// nono shell takes no program; it picks the shell with --shell.
	if mode == ModeShell && len(s.Command) > 0 {
		return errf("remove `command`, or set `shell_bin:` to choose the shell",
			"%s: `nono shell` takes no program", where)
	}
	return nil
}

// ValidEnvKey rejects names that cannot be represented in the K=V encoding
// execve uses. This has to be an error rather than a warning: there is no
// way to pass such a variable at all.
func ValidEnvKey(k string) error {
	if k == "" {
		return fmt.Errorf("must not be empty")
	}
	for i, c := range k {
		ok := c == '_' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(i > 0 && c >= '0' && c <= '9')
		if !ok {
			return fmt.Errorf("must match [A-Za-z_][A-Za-z0-9_]*")
		}
	}
	return nil
}
