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

	// Agents holds one section per agent, keyed by the name of its command.
	// A run applies the section of its agent only, so one project can run
	// several agents, each with its own pack and hosts.
	Agents map[string]Agent `toml:"agents"`

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

// Agent is an [agents.<name>] section. It adds to the [nono] section, except
// NetworkProfile, which replaces the one that [nono] names.
type Agent struct {
	Extends        []string `toml:"extends"`
	Groups         []string `toml:"groups"`
	AllowDomain    []string `toml:"allow_domain"`
	NetworkProfile string   `toml:"network_profile"`
	// Profile is a raw profile fragment for this agent only, for a need that
	// no other key covers, such as the local port of a language server.
	Profile toml.Primitive `toml:"profile"`
}

// WithAgent returns the [nono] section with an agent section applied.
func (n Nono) WithAgent(a Agent) Nono {
	n.Extends = append(append([]string{}, n.Extends...), a.Extends...)
	n.Groups = append(append([]string{}, n.Groups...), a.Groups...)
	n.AllowDomain = append(append([]string{}, n.AllowDomain...), a.AllowDomain...)
	if a.NetworkProfile != "" {
		n.NetworkProfile = a.NetworkProfile
	}
	return n
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
	return c.decodeProfile(c.Nono.Profile, "[nono.profile]")
}

// AgentProfile decodes the [agents.<name>.profile] block in the same way.
func (c *Config) AgentProfile(name string) (*nono.Profile, error) {
	return c.decodeProfile(c.Agents[name].Profile, "[agents."+name+".profile]")
}

func (c *Config) decodeProfile(prim toml.Primitive, label string) (*nono.Profile, error) {
	var raw map[string]any
	if err := c.md.PrimitiveDecode(prim, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	var p nono.Profile
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	return &p, nil
}
