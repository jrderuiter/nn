// Package config loads nn.toml.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/BurntSushi/toml"

	"github.com/jrderuiter/nn/internal/nono"
)

// Config is the project configuration.
type Config struct {
	// Nono holds everything that shapes the generated nono profile.
	Nono Nono `toml:"nono"`

	// Fnox says where secrets come from.
	Fnox Fnox `toml:"fnox"`

	// Root stops the upward search for a parent nn.toml.
	Root bool `toml:"root"`

	// Tools holds one lazily decoded sub-table per tool. Writing the section is
	// what turns the tool on, so a runtime that needs no settings is an empty
	// section.
	Tools map[string]toml.Primitive `toml:"tools"`

	md      toml.MetaData
	sources []string
}

// Nono is the [nono] section.
type Nono struct {
	// Extends adds nono profiles by name, which is how an agent pack such as
	// nolabs-ai/claude and hand written mixins such as jr/mise come in.
	Extends []string `toml:"extends"`
	// Groups adds nono policy groups by name.
	Groups []string `toml:"groups"`
	// AllowDomain adds hosts to the network allowlist, on top of whatever the
	// network profile and the tools allow. Wildcards follow nono's grammar,
	// where *.example.com matches one label or more below example.com.
	AllowDomain []string `toml:"allow_domain"`
	// NetworkProfile selects one of nono's built in network allowlists, such
	// as "developer" or "claude-code". Without one, nono leaves egress
	// unrestricted, so naming a profile narrows what the sandbox can reach.
	NetworkProfile string `toml:"network_profile"`
	// Profile is a raw profile fragment for anything that has no tool yet.
	// It applies after every tool, so a hand written rule always wins.
	// Its keys are spelled exactly as they are in a nono profile.
	Profile toml.Primitive `toml:"profile"`
}

type Fnox struct {
	Binary  string `toml:"binary"`
	Config  string `toml:"config"`
	Profile string `toml:"profile"`
}

// Meta exposes the decoder metadata, which providers need for a lazy decode of
// their own sub-table.
func (c *Config) Meta() toml.MetaData { return c.md }

// Sources lists the configuration files that contributed, in load order.
func (c *Config) Sources() []string { return c.sources }

// RawProfile decodes the [nono.profile] block. The block goes through JSON
// rather than straight into the struct, because the profile types are spelled
// for nono's own JSON schema. Decoding it as TOML would quietly miss every key
// whose name differs from its Go field, such as set_vars.
func (c *Config) RawProfile() (*nono.Profile, error) {
	var raw map[string]any
	if err := c.md.PrimitiveDecode(c.Nono.Profile, &raw); err != nil {
		return nil, fmt.Errorf("[nono.profile]: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("[nono.profile]: %w", err)
	}
	var p nono.Profile
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("[nono.profile]: %w", err)
	}
	return &p, nil
}
