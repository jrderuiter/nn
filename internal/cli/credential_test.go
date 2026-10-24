package cli

import (
	"strings"
	"testing"
)

// nono shows the stderr of a failed capture, so the error must say what exists.
func TestCredentialRejectsAnUnknownTool(t *testing.T) {
	root := newRoot(&options{})
	root.SetArgs([]string{"credential", "nope", "--flag"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "github") {
		t.Fatalf("got %v", err)
	}
}
