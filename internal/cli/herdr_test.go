package cli

import (
	"context"
	"slices"
	"testing"

	"github.com/jrderuiter/nn/internal/nono"
)

func buildInHerdr(t *testing.T, herdr string) *plan {
	t.Helper()
	o := caseOptions(t, "testdata/cases/all")
	o.getenv = func(name string) string {
		if name == "HERDR_ENV" {
			return herdr
		}
		return ""
	}
	p, err := build(context.Background(), o, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return p
}

// A hook inside the sandbox names its pane with HERDR_PANE_ID.
func TestHerdrPaneIDJoinsTheAllowlist(t *testing.T) {
	got := buildInHerdr(t, "1").profile.Environment.AllowVars
	if !slices.Contains(got, "HERDR_PANE_ID") {
		t.Fatalf("HERDR_PANE_ID is missing from %v", got)
	}
	// The socket and its API stay out of reach.
	for _, unwanted := range []string{"HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_AGENT"} {
		if slices.Contains(got, unwanted) {
			t.Errorf("%s must not reach the sandbox: %v", unwanted, got)
		}
	}
}

func TestOutsideHerdrTheAllowlistIsUnchanged(t *testing.T) {
	for _, value := range []string{"", "0"} {
		got := buildInHerdr(t, value).profile.Environment.AllowVars
		if slices.Contains(got, "HERDR_PANE_ID") {
			t.Errorf("HERDR_ENV=%q: did not expect HERDR_PANE_ID in %v", value, got)
		}
	}
}

// Without an allowlist nono passes every variable already. A list of only the
// pane id would strip all the others.
func TestHerdrNeverCreatesAnAllowlist(t *testing.T) {
	for _, p := range []*nono.Profile{
		{},
		{Environment: &nono.Environment{SetVars: map[string]string{"A": "b"}}},
	} {
		allowHerdrPane(p)
		if p.Environment != nil && len(p.Environment.AllowVars) != 0 {
			t.Errorf("an allowlist appeared: %v", p.Environment.AllowVars)
		}
	}
}

func TestHerdrPaneIDIsAddedOnce(t *testing.T) {
	p := &nono.Profile{Environment: &nono.Environment{AllowVars: []string{"PATH", "HERDR_PANE_ID"}}}
	allowHerdrPane(p)
	if got := p.Environment.AllowVars; len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}
