package cli

import (
	"runtime"
	"strings"
	"testing"
)

// A test binary carries no VCS stamp, so this covers the shape rather than the
// values a real build fills in.
func TestVersionStringAlwaysNamesTheVersionAndTheToolchain(t *testing.T) {
	got := versionString()
	if !strings.HasPrefix(got, "nn version ") {
		t.Fatalf("got:\n%s", got)
	}
	if !strings.Contains(got, runtime.Version()) {
		t.Fatalf("the Go version belongs in a bug report, got:\n%s", got)
	}
	if !strings.Contains(got, runtime.GOOS+"/"+runtime.GOARCH) {
		t.Fatalf("got:\n%s", got)
	}
}

// A build from source stamps no clock, so the line must stay away.
func TestVersionStringOmitsAnUnsetBuildTime(t *testing.T) {
	if strings.Contains(versionString(), "built") {
		t.Fatalf("got:\n%s", versionString())
	}
}

func TestVersionStringReportsABuildTimeWhenTheBuildSetOne(t *testing.T) {
	old := BuildTime
	t.Cleanup(func() { BuildTime = old })
	BuildTime = "2026-09-19T19:04:06Z"
	if !strings.Contains(versionString(), "built      2026-09-19T19:04:06Z") {
		t.Fatalf("got:\n%s", versionString())
	}
}
