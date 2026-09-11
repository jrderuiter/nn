package config

import "fmt"

// Mode is a nono execution mode. It is a bitmask so a flag spec can declare
// the set of modes that accept it in one field.
//
// The three modes are not interchangeable: `nono run` exposes 69 flags,
// `shell` 52 and `wrap` 36. Emitting a flag to a mode that does not accept it
// produces a clap error from a binary two levels down the exec chain, so nn
// validates against this matrix first.
type Mode uint8

const (
	ModeRun Mode = 1 << iota
	ModeShell
	ModeWrap

	ModeAll = ModeRun | ModeShell | ModeWrap
)

func (m Mode) String() string {
	switch m {
	case ModeRun:
		return "run"
	case ModeShell:
		return "shell"
	case ModeWrap:
		return "wrap"
	}
	return fmt.Sprintf("Mode(%d)", uint8(m))
}

// Names lists the modes in a bitmask, in run/shell/wrap order, for messages
// like "available in: run, shell".
func (m Mode) Names() []string {
	var out []string
	for _, c := range []Mode{ModeRun, ModeShell, ModeWrap} {
		if m&c != 0 {
			out = append(out, c.String())
		}
	}
	return out
}

// ParseMode maps a subcommand name to its mode.
func ParseMode(s string) (Mode, bool) {
	switch s {
	case "run":
		return ModeRun, true
	case "shell":
		return ModeShell, true
	case "wrap":
		return ModeWrap, true
	}
	return 0, false
}
