package cli

import (
	"strings"
	"testing"
)

func TestPrintVersion(t *testing.T) {
	defer func(v, c, d string) { Version, Commit, Date = v, c, d }(Version, Commit, Date)

	Version, Commit, Date = "v1.2.3", "abc123def456", "2026-09-11T12:00:00Z"
	var b strings.Builder
	if code := printVersion(&b); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	out := b.String()
	for _, want := range []string{"nn v1.2.3", "commit  abc123def456", "date    2026-09-11T12:00:00Z", "go      go1."} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// Outside a git checkout there is no commit to report, and the line should be
// omitted rather than printed empty.
func TestVersionOmitsAbsentFields(t *testing.T) {
	defer func(v, c, d string) { Version, Commit, Date = v, c, d }(Version, Commit, Date)

	Version, Commit, Date = "dev", "", ""
	var b strings.Builder
	printVersion(&b)
	out := b.String()
	if strings.Contains(out, "commit") {
		t.Errorf("commit line should be omitted:\n%s", out)
	}
	if !strings.HasPrefix(out, "nn dev\n") {
		t.Errorf("want a bare version line first:\n%s", out)
	}
	// The Go line is always available, from the runtime.
	if !strings.Contains(out, "go      go1.") {
		t.Errorf("go line missing:\n%s", out)
	}
}

func TestUserAgent(t *testing.T) {
	defer func(v, c string) { Version, Commit = v, c }(Version, Commit)

	Version, Commit = "v1.2.3", "abc123"
	if got := UserAgent(); got != "nn/v1.2.3+abc123" {
		t.Errorf("UserAgent = %q", got)
	}
	Version, Commit = "v1.2.3", ""
	if got := UserAgent(); got != "nn/1.2.3" {
		t.Errorf("UserAgent without commit = %q", got)
	}
}
