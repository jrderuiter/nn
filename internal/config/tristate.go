package config

import (
	"fmt"
	"strconv"

	"github.com/goccy/go-yaml"
)

// The three-valued types below distinguish "absent from the config" from an
// explicit zero value. That distinction is load-bearing in two places:
// overlaying CLI flags onto YAML (an unset flag must not clobber a YAML value),
// and nono flags where zero is meaningful — --startup-timeout 0 disables the
// check, which is not the same as leaving it unset.
//
// Each type also implements flag.Value, so wiring a CLI override is one line.
// They are comparable, which keeps table-test literals readable.

// Bool is a three-valued boolean. The zero value is unset.
type Bool struct {
	Present bool
	Value   bool
}

// TrueBool and FalseBool build set Bools, for tests and defaults.
func TrueBool() Bool  { return Bool{Present: true, Value: true} }
func FalseBool() Bool { return Bool{Present: true, Value: false} }

// Is reports whether b was set to v.
func (b Bool) Is(v bool) bool { return b.Present && b.Value == v }

// Or returns o when o is set, else b. Used to overlay a higher-precedence
// layer (CLI) onto a lower one (YAML).
func (b Bool) Or(o Bool) Bool {
	if o.Present {
		return o
	}
	return b
}

func (b *Bool) UnmarshalYAML(data []byte) error {
	var v bool
	if err := yaml.Unmarshal(data, &v); err != nil {
		return err
	}
	*b = Bool{Present: true, Value: v}
	return nil
}

func (b Bool) String() string {
	if !b.Present {
		return ""
	}
	return strconv.FormatBool(b.Value)
}

func (b *Bool) Set(s string) error {
	v, err := strconv.ParseBool(s)
	if err != nil {
		return fmt.Errorf("expected a boolean, got %q", s)
	}
	*b = Bool{Present: true, Value: v}
	return nil
}

// IsBoolFlag lets the flag package accept a bare --flag with no value.
func (b *Bool) IsBoolFlag() bool { return true }

// Neg adapts a *Bool so that a --no-x flag stores the negation. Registering
// both names against the same field means last-one-wins, as users expect.
type Neg struct{ B *Bool }

func (n Neg) String() string {
	if n.B == nil || !n.B.Present {
		return ""
	}
	return strconv.FormatBool(!n.B.Value)
}

func (n Neg) Set(s string) error {
	v, err := strconv.ParseBool(s)
	if err != nil {
		return fmt.Errorf("expected a boolean, got %q", s)
	}
	*n.B = Bool{Present: true, Value: !v}
	return nil
}

func (n Neg) IsBoolFlag() bool { return true }

// Int is a three-valued integer. The zero value is unset.
type Int struct {
	Present bool
	Value   int
}

func SomeInt(v int) Int { return Int{Present: true, Value: v} }

func (i Int) Or(o Int) Int {
	if o.Present {
		return o
	}
	return i
}

func (i *Int) UnmarshalYAML(data []byte) error {
	var v int
	if err := yaml.Unmarshal(data, &v); err != nil {
		return err
	}
	*i = Int{Present: true, Value: v}
	return nil
}

func (i Int) String() string {
	if !i.Present {
		return ""
	}
	return strconv.Itoa(i.Value)
}

func (i *Int) Set(s string) error {
	v, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("expected an integer, got %q", s)
	}
	*i = Int{Present: true, Value: v}
	return nil
}

// Count is an Int that also accepts a bool, so `verbose: true` means 1. It
// backs nono's repeatable -v, where both spellings read naturally.
type Count struct {
	Present bool
	Value   int
}

func SomeCount(v int) Count { return Count{Present: true, Value: v} }

func (c Count) Or(o Count) Count {
	if o.Present {
		return o
	}
	return c
}

func (c *Count) UnmarshalYAML(data []byte) error {
	var n int
	if err := yaml.Unmarshal(data, &n); err == nil {
		if n < 0 {
			return fmt.Errorf("expected a non-negative count, got %d", n)
		}
		*c = Count{Present: true, Value: n}
		return nil
	}
	var b bool
	if err := yaml.Unmarshal(data, &b); err != nil {
		return fmt.Errorf("expected a count or a boolean, got %s", data)
	}
	n = 0
	if b {
		n = 1
	}
	*c = Count{Present: true, Value: n}
	return nil
}

func (c Count) String() string {
	if !c.Present {
		return ""
	}
	return strconv.Itoa(c.Value)
}

func (c *Count) Set(s string) error {
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return fmt.Errorf("expected a non-negative count, got %q", s)
	}
	*c = Count{Present: true, Value: v}
	return nil
}

// Str is a three-valued string. The zero value is unset, which is distinct
// from an explicit empty string.
type Str struct {
	Present bool
	Value   string
}

func SomeStr(v string) Str { return Str{Present: true, Value: v} }

func (s Str) Or(o Str) Str {
	if o.Present {
		return o
	}
	return s
}

func (s *Str) UnmarshalYAML(data []byte) error {
	var v string
	if err := yaml.Unmarshal(data, &v); err != nil {
		return err
	}
	*s = Str{Present: true, Value: v}
	return nil
}

func (s Str) String() string { return s.Value }

func (s *Str) Set(v string) error {
	*s = Str{Present: true, Value: v}
	return nil
}
