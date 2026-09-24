package secrets

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeFnox writes a script that answers like fnox: GOOD resolves, EMPTY
// resolves to nothing, and any other key fails with a message on stderr.
func fakeFnox(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fnox")
	body := `#!/bin/sh
for last; do :; done
case "$last" in
GOOD) echo s3cret ;;
EMPTY) echo ;;
*) echo "no secret named $last" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGetReturnsTheValue(t *testing.T) {
	r := NewResolver(fakeFnox(t), "", "")
	got, err := r.Get(context.Background(), "GOOD")
	if err != nil || got != "s3cret" {
		t.Fatalf("got %q, %v", got, err)
	}
}

// fnox's own message is the useful part of the error, so it is kept.
func TestGetAndCheckReportTheFnoxError(t *testing.T) {
	r := NewResolver(fakeFnox(t), "", "")
	for name, err := range map[string]error{
		"get":   func() error { _, err := r.Get(context.Background(), "MISSING"); return err }(),
		"check": r.Check(context.Background(), "MISSING"),
	} {
		if err == nil || !strings.Contains(err.Error(), "no secret named MISSING") {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

// An empty value would start the agent with a credential that fails on first
// use, so it is an error in both calls.
func TestAnEmptyValueIsAnError(t *testing.T) {
	r := NewResolver(fakeFnox(t), "", "")
	if _, err := r.Get(context.Background(), "EMPTY"); err == nil {
		t.Error("get: an empty value must be an error")
	}
	if err := r.Check(context.Background(), "EMPTY"); err == nil {
		t.Error("check: an empty value must be an error")
	}
}

func TestCheckPassesForAKeyThatResolves(t *testing.T) {
	r := NewResolver(fakeFnox(t), "", "")
	if err := r.Check(context.Background(), "GOOD"); err != nil {
		t.Fatal(err)
	}
}

func TestAMissingBinaryIsAnError(t *testing.T) {
	r := NewResolver(filepath.Join(t.TempDir(), "no-fnox"), "", "")
	if err := r.Check(context.Background(), "GOOD"); err == nil {
		t.Fatal("a missing fnox must be an error")
	}
}
