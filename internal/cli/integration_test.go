//go:build integration

// These tests need the real nono binary. Run them with:
//
//	go test -tags integration ./...
package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Every generated profile must satisfy nono's own schema, including the strict
// check that turns a deprecated key into a failure.
func TestGoldenProfilesValidate(t *testing.T) {
	requireNono(t)
	files, _ := filepath.Glob("testdata/golden/*.json")
	if len(files) == 0 {
		t.Fatal("no golden profiles found")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			out, err := exec.Command("nono", "profile", "validate", f, "--json", "--strict").Output()
			if err != nil && len(out) == 0 {
				t.Fatalf("nono profile validate failed: %v", err)
			}
			var v struct {
				Valid  bool     `json:"valid"`
				Errors []string `json:"errors"`
			}
			if err := json.Unmarshal(out, &v); err != nil {
				t.Fatalf("cannot read validate output: %v", err)
			}
			if !v.Valid {
				t.Fatalf("profile is not valid: %v", v.Errors)
			}
		})
	}
}

// A nono upgrade that renames or removes a key must fail here rather than in
// front of a user.
func TestSchemaStillHasTheKeysWeGenerate(t *testing.T) {
	requireNono(t)
	out, err := exec.Command("nono", "profile", "schema").Output()
	if err != nil {
		t.Fatalf("nono profile schema failed: %v", err)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(out, &schema); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"extends", "meta", "groups", "filesystem", "network",
		"environment", "workdir", "credential_capture", "packs",
	} {
		if _, ok := schema.Properties[key]; !ok {
			t.Errorf("nono no longer has the top level key %q that nn generates", key)
		}
	}
}

func requireNono(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("nono"); err != nil {
		// A skip in CI would pass the job without checking anything.
		if os.Getenv("CI") != "" {
			t.Fatal("nono is not installed")
		}
		t.Skip("nono is not installed")
	}
}
