package cli

import (
	"testing"

	"github.com/jrderuiter/nn/internal/tool"
)

// Entries from every tool share one numbered list, in the order given, so a
// repeated key such as an insteadOf rewrite keeps each of its values.
func TestGitConfigIsNumberedOnce(t *testing.T) {
	vars := gitConfigFragment([]tool.GitConfig{
		{Key: "url.a.insteadOf", Value: "x"},
		{Key: "url.a.insteadOf", Value: "y"},
		{Key: "url.b.insteadOf", Value: "z"},
	}).Environment.SetVars
	want := map[string]string{
		"GIT_CONFIG_COUNT":   "3",
		"GIT_CONFIG_KEY_0":   "url.a.insteadOf",
		"GIT_CONFIG_VALUE_0": "x",
		"GIT_CONFIG_KEY_1":   "url.a.insteadOf",
		"GIT_CONFIG_VALUE_1": "y",
		"GIT_CONFIG_KEY_2":   "url.b.insteadOf",
		"GIT_CONFIG_VALUE_2": "z",
	}
	if len(vars) != len(want) {
		t.Fatalf("got %v", vars)
	}
	for k, v := range want {
		if vars[k] != v {
			t.Errorf("%s = %q, want %q", k, vars[k], v)
		}
	}
}
