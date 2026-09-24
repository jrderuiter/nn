package nono

import (
	"fmt"
	"reflect"
)

// Merger folds tool fragments onto a base profile.
//
// nn merges only fragments that it generated itself, so a collision means two
// tools disagree about the same key. That is a configuration error and
// the merger reports it instead of picking a winner.
type Merger struct {
	dst   *Profile
	owner map[string]string // key path to the tool that set it
	// override lets the current layer replace a value that an earlier layer
	// set, instead of reporting a conflict. Only the user's own raw block uses
	// it, because that block is the last word by design.
	override bool
}

func NewMerger(base *Profile) *Merger {
	if base == nil {
		base = &Profile{}
	}
	return &Merger{dst: base, owner: map[string]string{}}
}

// Profile returns the merged result.
func (m *Merger) Profile() *Profile { return m.dst }

// AddOverride folds src in and lets it replace what earlier layers set. It is
// for the user's own raw profile block, which is meant to have the last word.
func (m *Merger) AddOverride(src *Profile, name string) error {
	m.override = true
	defer func() { m.override = false }()
	return m.Add(src, name)
}

// Add folds src into the destination. name identifies the contributing
// tool in error messages.
func (m *Merger) Add(src *Profile, name string) error {
	if src == nil {
		return nil
	}
	d := m.dst

	d.Extends = appendUnique(d.Extends, src.Extends, func(s string) string { return s })
	d.Packs = appendUnique(d.Packs, src.Packs, func(s string) string { return s })

	if src.Groups != nil {
		if d.Groups == nil {
			d.Groups = &Groups{}
		}
		d.Groups.Include = appendUnique(d.Groups.Include, src.Groups.Include, condNameKey)
		d.Groups.Exclude = appendUnique(d.Groups.Exclude, src.Groups.Exclude, condNameKey)
	}

	if src.Filesystem != nil {
		if d.Filesystem == nil {
			d.Filesystem = &Filesystem{}
		}
		mergeFilesystem(d.Filesystem, src.Filesystem)
	}

	if src.Environment != nil {
		if d.Environment == nil {
			d.Environment = &Environment{}
		}
		d.Environment.AllowVars = appendUnique(d.Environment.AllowVars, src.Environment.AllowVars, func(s string) string { return s })
		d.Environment.DenyVars = appendUnique(d.Environment.DenyVars, src.Environment.DenyVars, func(s string) string { return s })
		if err := mergeMap(m, &d.Environment.SetVars, src.Environment.SetVars, "environment.set_vars", name); err != nil {
			return err
		}
	}

	if err := mergeMap(m, &d.CredentialCapture, src.CredentialCapture, "credential_capture", name); err != nil {
		return err
	}

	if src.Network != nil {
		if d.Network == nil {
			d.Network = &Network{}
		}
		if err := m.mergeNetwork(d.Network, src.Network, name); err != nil {
			return err
		}
	}

	if src.Workdir != nil && src.Workdir.Access != "" {
		if d.Workdir == nil {
			d.Workdir = &Workdir{}
		}
		if err := m.setScalar(&d.Workdir.Access, src.Workdir.Access, "workdir.access", name); err != nil {
			return err
		}
	}

	if src.Meta != nil {
		if d.Meta == nil {
			d.Meta = &Meta{}
		}
		for _, f := range []struct {
			dst *string
			src string
			key string
		}{
			{&d.Meta.Name, src.Meta.Name, "meta.name"},
			{&d.Meta.Version, src.Meta.Version, "meta.version"},
			{&d.Meta.Description, src.Meta.Description, "meta.description"},
			{&d.Meta.Author, src.Meta.Author, "meta.author"},
		} {
			if f.src == "" {
				continue
			}
			if err := m.setScalar(f.dst, f.src, f.key, name); err != nil {
				return err
			}
		}
	}

	if src.Schema != "" && d.Schema == "" {
		d.Schema = src.Schema
	}
	return nil
}

func (m *Merger) mergeNetwork(d, s *Network, name string) error {
	// network.block is sticky true: once any layer blocks the network, no
	// later fragment may open it again. nono applies the same rule to extends.
	if s.Block != nil && *s.Block {
		t := true
		d.Block = &t
	} else if s.Block != nil && d.Block == nil {
		f := false
		d.Block = &f
	}

	if s.TLSIntercept != nil && s.TLSIntercept.CALifecycle == "" && s.TLSIntercept.CAValidity == "" {
		s.TLSIntercept = nil
	}

	if s.NetworkProfile != "" {
		if err := m.setScalar(&d.NetworkProfile, s.NetworkProfile, "network.network_profile", name); err != nil {
			return err
		}
	}

	d.DenyDomain = appendUnique(d.DenyDomain, s.DenyDomain, func(v string) string { return v })
	d.Credentials = appendUnique(d.Credentials, s.Credentials, func(v string) string { return v })
	d.NoProxy = appendUnique(d.NoProxy, s.NoProxy, func(v string) string { return v })
	d.OpenPort = appendUnique(d.OpenPort, s.OpenPort, func(v int) string { return fmt.Sprint(v) })
	d.OpenPortRange = appendUnique(d.OpenPortRange, s.OpenPortRange, func(v [2]int) string { return fmt.Sprint(v) })
	d.ListenPort = appendUnique(d.ListenPort, s.ListenPort, func(v int) string { return fmt.Sprint(v) })
	d.AllowDomain = mergeDomains(d.AllowDomain, s.AllowDomain)

	if s.TLSIntercept != nil {
		if d.TLSIntercept == nil {
			d.TLSIntercept = &TLSIntercept{}
		}
		if err := m.setScalar(&d.TLSIntercept.CALifecycle, s.TLSIntercept.CALifecycle,
			"network.tls_intercept.ca_lifecycle", name); err != nil {
			return err
		}
		d.TLSIntercept.CAEnvVars = appendUnique(d.TLSIntercept.CAEnvVars, s.TLSIntercept.CAEnvVars,
			func(v string) string { return v })
	}

	return mergeMap(m, &d.CustomCredentials, s.CustomCredentials, "network.custom_credentials", name)
}

func mergeFilesystem(d, s *Filesystem) {
	pairs := []struct{ dst, src *[]CondPath }{
		{&d.Allow, &s.Allow},
		{&d.Read, &s.Read},
		{&d.Write, &s.Write},
		{&d.AllowFile, &s.AllowFile},
		{&d.ReadFile, &s.ReadFile},
		{&d.WriteFile, &s.WriteFile},
		{&d.UnixSocket, &s.UnixSocket},
		{&d.UnixSocketDir, &s.UnixSocketDir},
		{&d.UnixSocketSubtree, &s.UnixSocketSubtree},
		{&d.Deny, &s.Deny},
		{&d.BypassProtection, &s.BypassProtection},
	}
	for _, p := range pairs {
		*p.dst = appendUnique(*p.dst, *p.src, condPathKey)
	}
}

// mergeDomains appends new domains and concatenates the endpoint rules of a
// domain that both sides mention, which is what nono does across extends.
func mergeDomains(dst, src []Domain) []Domain {
	idx := map[string]int{}
	for i, d := range dst {
		idx[d.Domain] = i
	}
	for _, s := range src {
		i, ok := idx[s.Domain]
		if !ok {
			dst = append(dst, s)
			idx[s.Domain] = len(dst) - 1
			continue
		}
		dst[i].Endpoints = appendUnique(dst[i].Endpoints, s.Endpoints, endpointKey)
	}
	return dst
}

// mergeMap folds src into *dst. A key present on both sides with a different
// value is a conflict, and an identical value is a silent no-op. It is a free
// function because Go methods cannot take type parameters.
func mergeMap[V any](m *Merger, dst *map[string]V, src map[string]V, field, name string) error {
	for k, v := range src {
		full := field + "[" + k + "]"
		if *dst == nil {
			*dst = map[string]V{}
		}
		if old, ok := (*dst)[k]; ok && !reflect.DeepEqual(old, v) && !m.override {
			return mergeMapConflict(m, full, name)
		}
		(*dst)[k] = v
		m.claim(full, name)
	}
	return nil
}

func (m *Merger) setScalar(dst *string, v, field, name string) error {
	if *dst != "" && *dst != v && !m.override {
		return m.conflict(field, name)
	}
	*dst = v
	m.claim(field, name)
	return nil
}

func mergeMapConflict(m *Merger, field, name string) error { return m.conflict(field, name) }

func (m *Merger) claim(field, name string) {
	if _, ok := m.owner[field]; !ok {
		m.owner[field] = name
	}
}

func (m *Merger) conflict(field, name string) error {
	prev, ok := m.owner[field]
	if !ok {
		prev = "the base profile"
	}
	return fmt.Errorf("%q and %q both set %s to different values; "+
		"give one of them a distinct name or remove the overlap", prev, name, field)
}

// appendUnique appends the items of src that dst does not already carry,
// keeping first-seen order so generated profiles are byte stable.
func appendUnique[T any](dst, src []T, key func(T) string) []T {
	if len(src) == 0 {
		return dst
	}
	seen := make(map[string]struct{}, len(dst)+len(src))
	for _, v := range dst {
		seen[key(v)] = struct{}{}
	}
	for _, v := range src {
		k := key(v)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		dst = append(dst, v)
	}
	return dst
}

func condPathKey(c CondPath) string { return c.Path + "\x00" + joinWhen(c.When) }
func condNameKey(c CondName) string { return c.Name + "\x00" + joinWhen(c.When) }
func endpointKey(e Endpoint) string { return e.Method + "\x00" + e.Path }

func joinWhen(w []string) string {
	out := ""
	for _, s := range w {
		out += s + ","
	}
	return out
}
