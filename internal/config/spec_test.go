package config

import (
	"os/exec"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// Every Section field must be emitted by exactly one spec, or be declared
// nn-only. Without this, adding a field and forgetting to emit it is silent.
func TestSpecCoverage(t *testing.T) {
	inSpec := map[string]int{}
	for _, s := range Specs {
		inSpec[s.Key]++
	}
	for key, n := range inSpec {
		if n > 1 {
			t.Errorf("key %q appears in %d specs, want 1", key, n)
		}
	}

	rt := reflect.TypeOf(Section{})
	for i := range rt.NumField() {
		key := strings.Split(rt.Field(i).Tag.Get("yaml"), ",")[0]
		if key == "" || key == "-" {
			continue
		}
		_, spec := inSpec[key]
		nnOnly := slices.Contains(NNOnlyKeys, key)
		switch {
		case spec && nnOnly:
			t.Errorf("key %q is both in Specs and NNOnlyKeys", key)
		case !spec && !nnOnly:
			t.Errorf("key %q (field %s) is in neither Specs nor NNOnlyKeys", key, rt.Field(i).Name)
		}
	}

	for _, key := range NNOnlyKeys {
		if _, ok := rt.FieldByNameFunc(func(name string) bool {
			f, _ := rt.FieldByName(name)
			return strings.Split(f.Tag.Get("yaml"), ",")[0] == key
		}); !ok {
			t.Errorf("NNOnlyKeys lists %q, which is not a Section field", key)
		}
	}
}

func TestSpecInvariants(t *testing.T) {
	for _, s := range Specs {
		if s.Modes == 0 {
			t.Errorf("%s: no modes", s.Key)
		}
		if s.Get == nil {
			t.Errorf("%s: nil Get", s.Key)
		}
		if !strings.HasPrefix(s.Flag, "--") {
			t.Errorf("%s: flag %q should start with --", s.Key, s.Flag)
		}
		// Only value-carrying flags can name a path.
		if s.Anchor != AnchorNone && s.Kind != KindString && s.Kind != KindStringSlice {
			t.Errorf("%s: anchored but kind is not string-ish", s.Key)
		}
	}
}

// The matrix in Specs was generated from nono's help output. If nono changes
// it underneath us, emission breaks in a way that surfaces as a clap error
// two processes down the chain. Re-derive it here so drift fails loudly.
func TestModeMatrixMatchesInstalledNono(t *testing.T) {
	if _, err := exec.LookPath("nono"); err != nil {
		t.Skip("nono not on PATH")
	}
	flagRE := regexp.MustCompile(`(?m)^\s{2,6}(?:-[a-zA-Z], )?(--[a-z0-9-]+)`)

	actual := map[string]Mode{}
	for _, m := range []Mode{ModeRun, ModeShell, ModeWrap} {
		out, err := exec.Command("nono", m.String(), "--help").CombinedOutput()
		if err != nil {
			t.Fatalf("nono %s --help: %v", m, err)
		}
		for _, mt := range flagRE.FindAllStringSubmatch(string(out), -1) {
			actual[mt[1]] |= m
		}
	}

	var problems []string
	for _, s := range Specs {
		got, ok := actual[s.Flag]
		if !ok {
			problems = append(problems, s.Flag+": not offered by any mode of the installed nono")
			continue
		}
		if got != s.Modes {
			problems = append(problems, s.Flag+": spec says ["+strings.Join(s.Modes.Names(), " ")+
				"], nono offers ["+strings.Join(got.Names(), " ")+"]")
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Errorf("mode matrix drift: %s", p)
	}
	if len(problems) > 0 {
		out, _ := exec.Command("nono", "--version").Output()
		t.Logf("installed: %s", strings.TrimSpace(string(out)))
	}
}
