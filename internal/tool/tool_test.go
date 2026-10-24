package tool

import (
	"context"
	"io"
	"testing"
)

func TestRegisterCredential(t *testing.T) {
	t.Cleanup(func() { delete(credentials, "probe") })
	f := func(context.Context, []string, io.Writer) error { return nil }
	RegisterCredential("probe", f)
	if _, ok := Credential("probe"); !ok {
		t.Fatal("a registered handler must be found")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("a second handler for one tool must panic")
		}
	}()
	RegisterCredential("probe", f)
}
