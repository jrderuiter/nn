package config

import (
	"flag"
	"testing"

	"github.com/goccy/go-yaml"
)

func TestTristateYAML(t *testing.T) {
	type doc struct {
		A Bool  `yaml:"a"`
		B Bool  `yaml:"b"`
		C Bool  `yaml:"c"`
		N Int   `yaml:"n"`
		Z Int   `yaml:"z"`
		V Count `yaml:"v"`
		W Count `yaml:"w"`
		S Str   `yaml:"s"`
		E Str   `yaml:"e"`
	}
	var d doc
	src := "a: true\nb: false\nn: 5\nz: 0\nv: 2\nw: true\ns: hello\ne: \"\"\n"
	if err := yaml.Unmarshal([]byte(src), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !d.A.Is(true) {
		t.Errorf("a: want set-true, got %+v", d.A)
	}
	if !d.B.Is(false) {
		t.Errorf("b: want set-false, got %+v", d.B)
	}
	if d.C.Present {
		t.Errorf("c: want unset, got %+v", d.C)
	}
	if d.N != SomeInt(5) {
		t.Errorf("n: want 5, got %+v", d.N)
	}
	// The whole point of the type: explicit 0 must not look like absent.
	if d.Z != SomeInt(0) {
		t.Errorf("z: want set-0, got %+v", d.Z)
	}
	if d.V != SomeCount(2) {
		t.Errorf("v: want 2, got %+v", d.V)
	}
	if d.W != SomeCount(1) {
		t.Errorf("w: want bool sugar -> 1, got %+v", d.W)
	}
	if d.S != SomeStr("hello") {
		t.Errorf("s: want hello, got %+v", d.S)
	}
	if d.E != SomeStr("") {
		t.Errorf("e: want set-empty, got %+v", d.E)
	}
}

func TestTristateFlags(t *testing.T) {
	var allowCwd Bool
	var timeout Int
	var verbose Count

	fs := flag.NewFlagSet("nn", flag.ContinueOnError)
	fs.Var(&allowCwd, "allow-cwd", "")
	fs.Var(Neg{&allowCwd}, "no-allow-cwd", "")
	fs.Var(&timeout, "startup-timeout", "")
	fs.Var(&verbose, "verbose", "")

	if err := fs.Parse([]string{"--allow-cwd", "--startup-timeout", "0", "--verbose", "3"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !allowCwd.Is(true) {
		t.Errorf("--allow-cwd: want true, got %+v", allowCwd)
	}
	if timeout != SomeInt(0) {
		t.Errorf("--startup-timeout 0: want set-0, got %+v", timeout)
	}
	if verbose != SomeCount(3) {
		t.Errorf("--verbose 3: want 3, got %+v", verbose)
	}

	// --no-x must store false, not merely fail to set true.
	var b Bool
	fs2 := flag.NewFlagSet("nn", flag.ContinueOnError)
	fs2.Var(&b, "allow-cwd", "")
	fs2.Var(Neg{&b}, "no-allow-cwd", "")
	if err := fs2.Parse([]string{"--no-allow-cwd"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !b.Is(false) {
		t.Errorf("--no-allow-cwd: want set-false, got %+v", b)
	}
}

func TestOrOverlay(t *testing.T) {
	yamlVal := TrueBool()
	if got := yamlVal.Or(Bool{}); !got.Is(true) {
		t.Errorf("unset CLI must not clobber YAML: got %+v", got)
	}
	if got := yamlVal.Or(FalseBool()); !got.Is(false) {
		t.Errorf("explicit CLI false must win: got %+v", got)
	}
}
