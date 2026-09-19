package cli

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// Version is set at build time with -ldflags. A build that does not set it
// falls back to what the Go toolchain recorded, which for `go install
// github.com/jrderuiter/nn@latest` is the module version.
var Version = "dev"

// BuildTime is set at build time with -ldflags, as an RFC 3339 timestamp.
//
// It stays empty for a build from source, on purpose. The same source then
// always produces the same binary, and the commit time below already says when
// the code is from. A clock reading would only say when someone last typed
// `go build`.
var BuildTime = ""

// versionString describes the binary as precisely as the build allows.
func versionString() string {
	version, commit, commitTime := Version, "", ""
	if info, ok := debug.ReadBuildInfo(); ok {
		var modified bool
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				commit = s.Value
			case "vcs.time":
				commitTime = s.Value
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
		if len(commit) > 12 {
			commit = commit[:12]
		}
		if commit != "" && modified {
			// The tree had uncommitted changes, so the revision names the
			// commit this was built near, not the code that is in it.
			commit += "-dirty"
		}
		// A build from a checkout records a pseudo-version that repeats the
		// commit line below, so it is only worth reading when there is no
		// checkout: that is `go install module@version`, which records the
		// released version and no revision.
		if version == "dev" && commit == "" &&
			info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "nn version %s\n", version)
	if commit != "" {
		fmt.Fprintf(&b, "commit     %s", commit)
		if commitTime != "" {
			fmt.Fprintf(&b, " (%s)", commitTime)
		}
		b.WriteString("\n")
	}
	if BuildTime != "" {
		fmt.Fprintf(&b, "built      %s\n", BuildTime)
	}
	fmt.Fprintf(&b, "go         %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return b.String()
}
