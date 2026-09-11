package config

import (
	"reflect"
	"sort"
)

// isSet reports whether a Section field carries a value. Tri-state types know
// themselves; slices and maps count as set when non-nil, so an explicit empty
// list in a mode block still overrides an inherited one.
func isSet(v reflect.Value) bool {
	switch x := v.Interface().(type) {
	case Bool:
		return x.Present
	case Int:
		return x.Present
	case Count:
		return x.Present
	case Str:
		return x.Present
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Map:
		return !v.IsNil()
	}
	return false
}

// IsKeySet reports whether the named YAML key carries a value in s.
func IsKeySet(s *Section, key string) bool {
	f := fieldByKey(reflect.ValueOf(s).Elem(), key)
	if !f.IsValid() {
		return false
	}
	return isSet(f)
}

func fieldByKey(sv reflect.Value, key string) reflect.Value {
	rt := sv.Type()
	for i := range rt.NumField() {
		if yamlKey(rt.Field(i)) == key {
			return sv.Field(i)
		}
	}
	return reflect.Value{}
}

func yamlKey(f reflect.StructField) string {
	tag := f.Tag.Get("yaml")
	for i, c := range tag {
		if c == ',' {
			return tag[:i]
		}
	}
	return tag
}

// SetKeys lists the YAML keys carrying a value in s, in schema order.
func SetKeys(s *Section) []string {
	var out []string
	sv := reflect.ValueOf(s).Elem()
	rt := sv.Type()
	for i := range rt.NumField() {
		key := yamlKey(rt.Field(i))
		if key == "" || key == "-" {
			continue
		}
		if isSet(sv.Field(i)) {
			out = append(out, key)
		}
	}
	return out
}

// Resolve merges the mode's block over the top-level section.
//
// Two things happen here, and the asymmetry between them is deliberate.
//
// Merge: a value set in the block replaces the top-level one outright —
// scalars and slices alike. That differs from nono's own profile `extends`,
// which appends arrays, and the difference is intentional: nono's arrays are
// capability grants, where appending is the safe direction, while nn's are
// argv fragments, where `wrap: {command: [go, test]}` has to mean "instead
// of", not "in addition to". The exceptions are env, which merges per key, and
// env_unset, which unions, because both are sets rather than sequences.
//
// Filtering: a top-level key the mode does not accept is dropped and
// reported, not rejected. That is what lets one file serve `nn` and `nn wrap`
// without editing. Keys inside a mode block get the opposite treatment — see
// Validate, which rejects them, because naming the block asserts the key
// belongs to that mode.
func Resolve(f *File, mode Mode) (*Section, []string) {
	merged := f.Section
	if b := f.Block(mode); b != nil {
		mergeInto(&merged, b)
	}

	var dropped []string
	for _, spec := range Specs {
		if spec.Modes&mode != 0 {
			continue
		}
		// Only a top-level key can reach here; Validate rejects block keys.
		if IsKeySet(&merged, spec.Key) {
			clearKey(&merged, spec.Key)
			dropped = append(dropped, spec.Key)
		}
	}
	sort.Strings(dropped)
	return &merged, dropped
}

func mergeInto(dst, src *Section) {
	dv := reflect.ValueOf(dst).Elem()
	sv := reflect.ValueOf(src).Elem()
	rt := dv.Type()

	for i := range rt.NumField() {
		key := yamlKey(rt.Field(i))
		sf, df := sv.Field(i), dv.Field(i)

		if key == "env" {
			df.Set(mergeStringMap(df, sf))
			continue
		}
		if key == "env_unset" {
			df.Set(unionStrings(df, sf))
			continue
		}
		if isSet(sf) {
			df.Set(sf)
		}
	}
}

func mergeStringMap(dst, src reflect.Value) reflect.Value {
	if src.IsNil() {
		return dst
	}
	out := reflect.MakeMap(dst.Type())
	for _, m := range []reflect.Value{dst, src} {
		if m.IsNil() {
			continue
		}
		for _, k := range m.MapKeys() {
			out.SetMapIndex(k, m.MapIndex(k))
		}
	}
	return out
}

func unionStrings(dst, src reflect.Value) reflect.Value {
	if src.IsNil() {
		return dst
	}
	seen := map[string]bool{}
	out := reflect.MakeSlice(dst.Type(), 0, dst.Len()+src.Len())
	for _, s := range []reflect.Value{dst, src} {
		for i := range s.Len() {
			v := s.Index(i).String()
			if seen[v] {
				continue
			}
			seen[v] = true
			out = reflect.Append(out, s.Index(i))
		}
	}
	return out
}

func clearKey(s *Section, key string) {
	f := fieldByKey(reflect.ValueOf(s).Elem(), key)
	if f.IsValid() {
		f.Set(reflect.Zero(f.Type()))
	}
}
