package cli

import (
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"strings"
)

// Build metadata, injected with -ldflags. Empty in a binary built without
// them — notably one from `go install`, which is why each of these falls back
// to the VCS stamps the Go toolchain embeds on its own.
var (
	Version = "dev"
	Commit  = ""
	// Date is the SOURCE date — the commit time, or SOURCE_DATE_EPOCH — not
	// the moment the compiler ran. Stamping a wall clock would make every
	// build of identical source produce a different binary.
	Date = ""
)

func printVersion(w io.Writer) int {
	v, commit, date, dirty := buildInfo()

	fmt.Fprintf(w, "nn %s\n", v)
	if commit != "" {
		if dirty {
			commit += " (dirty)"
		}
		fmt.Fprintf(w, "commit  %s\n", commit)
	}
	if date != "" {
		fmt.Fprintf(w, "date    %s\n", date)
	}
	fmt.Fprintf(w, "go      %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return ExitOK
}

// buildInfo prefers the ldflags values and falls back to the toolchain's
// embedded VCS stamps, so `go install ...@latest` still reports something
// useful rather than a bare "dev".
func buildInfo() (version, commit, date string, dirty bool) {
	version, commit, date = Version, Commit, Date

	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return version, commit, date, false
	}
	if version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		version = bi.Main.Version
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if commit == "" {
				commit = short(s.Value)
			}
		case "vcs.time":
			if date == "" {
				date = s.Value
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return version, commit, date, dirty
}

func short(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

// UserAgent is a one-line form, for diagnostics.
func UserAgent() string {
	v, commit, _, _ := buildInfo()
	if commit != "" {
		return "nn/" + v + "+" + commit
	}
	return "nn/" + strings.TrimPrefix(v, "v")
}
