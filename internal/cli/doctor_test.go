package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrderuiter/nn/internal/config"
)

// doctor warns about a section whose tool is not enabled. The enabled tools,
// and the list itself, must not appear.
func TestIdleListsConfiguredToolsThatAreNotEnabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, config.FileName)
	body := "[tools]\nenabled = [\"git\"]\n\n[tools.git]\nname = \"J\"\n\n[tools.kubernetes]\ncontext = \"x\"\n\n[tools.github]\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.Options{Dir: dir, Explicit: path})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(idle(cfg), ","); got != "github,kubernetes" {
		t.Fatalf("got %q", got)
	}
}
