// Package nono models the subset of the nono profile schema that nn generates,
// and knows how to merge fragments and invoke the nono binary.
package nono

import (
	"encoding/json"
	"fmt"
)

// SchemaURL is the canonical schema identifier emitted by `nono profile schema`.
const SchemaURL = "https://nono.sh/schemas/nono-profile.schema.json"

// Profile is the generated nono profile. Every field is omitempty because the
// nono schema sets additionalProperties:false and treats a null or unexpected
// key as a hard error.
type Profile struct {
	Schema            string                       `json:"$schema,omitempty"`
	Extends           []string                     `json:"extends,omitempty"`
	Meta              *Meta                        `json:"meta,omitempty"`
	Packs             []string                     `json:"packs,omitempty"`
	Groups            *Groups                      `json:"groups,omitempty"`
	Filesystem        *Filesystem                  `json:"filesystem,omitempty"`
	Network           *Network                     `json:"network,omitempty"`
	Environment       *Environment                 `json:"environment,omitempty"`
	Workdir           *Workdir                     `json:"workdir,omitempty"`
	CredentialCapture map[string]CredentialCapture `json:"credential_capture,omitempty"`
}

type Meta struct {
	Name        string `json:"name,omitempty"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
}

type Groups struct {
	Include []CondName `json:"include,omitempty"`
	Exclude []CondName `json:"exclude,omitempty"`
}

type Filesystem struct {
	Allow             []CondPath `json:"allow,omitempty"`
	Read              []CondPath `json:"read,omitempty"`
	Write             []CondPath `json:"write,omitempty"`
	AllowFile         []CondPath `json:"allow_file,omitempty"`
	ReadFile          []CondPath `json:"read_file,omitempty"`
	WriteFile         []CondPath `json:"write_file,omitempty"`
	UnixSocket        []CondPath `json:"unix_socket,omitempty"`
	UnixSocketDir     []CondPath `json:"unix_socket_dir,omitempty"`
	UnixSocketSubtree []CondPath `json:"unix_socket_subtree,omitempty"`
	Deny              []CondPath `json:"deny,omitempty"`
	BypassProtection  []CondPath `json:"bypass_protection,omitempty"`
}

type Network struct {
	Block             *bool                       `json:"block,omitempty"`
	NetworkProfile    string                      `json:"network_profile,omitempty"`
	AllowDomain       []Domain                    `json:"allow_domain,omitempty"`
	DenyDomain        []string                    `json:"deny_domain,omitempty"`
	Credentials       []string                    `json:"credentials,omitempty"`
	OpenPort          []int                       `json:"open_port,omitempty"`
	OpenPortRange     [][2]int                    `json:"open_port_range,omitempty"`
	ListenPort        []int                       `json:"listen_port,omitempty"`
	NoProxy           []string                    `json:"no_proxy,omitempty"`
	CustomCredentials map[string]CustomCredential `json:"custom_credentials,omitempty"`
	TLSIntercept      *TLSIntercept               `json:"tls_intercept,omitempty"`
}

// TLSIntercept controls the certificate that nono presents when it intercepts
// a connection in order to inject a credential.
type TLSIntercept struct {
	// CALifecycle is "session" for a per-run authority exposed through the
	// trust bundle variables, or "trusted" for a reusable one in the macOS
	// user trust store.
	CALifecycle  string   `json:"ca_lifecycle,omitempty"`
	CAValidity   string   `json:"ca_validity,omitempty"`
	LeafValidity string   `json:"leaf_validity,omitempty"`
	CAEnvVars    []string `json:"ca_env_vars,omitempty"`
}

// CustomCredential is a proxy route. nono only activates it when its map key
// also appears in Network.Credentials.
type CustomCredential struct {
	Upstream      string     `json:"upstream"`
	CredentialKey string     `json:"credential_key,omitempty"`
	EnvVar        string     `json:"env_var,omitempty"`
	InjectMode    string     `json:"inject_mode,omitempty"`
	InjectHeader  string     `json:"inject_header,omitempty"`
	CredentialFmt string     `json:"credential_format,omitempty"`
	EndpointRules []Endpoint `json:"endpoint_rules,omitempty"`
	TLSCA         string     `json:"tls_ca,omitempty"`
	TLSClientCert string     `json:"tls_client_cert,omitempty"`
	TLSClientKey  string     `json:"tls_client_key,omitempty"`
}

type Endpoint struct {
	Method string `json:"method,omitempty"`
	Path   string `json:"path"`
	Reason string `json:"reason,omitempty"`
}

// CredentialCapture runs a command on the host to produce a credential. nono
// caches the result and injects it through the proxy, so the value never
// reaches the sandboxed process.
type CredentialCapture struct {
	Command      []string `json:"command"`
	TimeoutSecs  int      `json:"timeout_secs,omitempty"`
	CacheTTLSecs int      `json:"cache_ttl_secs,omitempty"`
	TTLSecs      int      `json:"ttl_secs,omitempty"`
}

type Environment struct {
	AllowVars []string          `json:"allow_vars,omitempty"`
	DenyVars  []string          `json:"deny_vars,omitempty"`
	SetVars   map[string]string `json:"set_vars,omitempty"`
}

type Workdir struct {
	Access string `json:"access,omitempty"`
}

// Domain is either a bare hostname or an object carrying endpoint rules.
type Domain struct {
	Domain    string
	Endpoints []Endpoint
}

func (d Domain) MarshalJSON() ([]byte, error) {
	if len(d.Endpoints) == 0 {
		return json.Marshal(d.Domain)
	}
	return json.Marshal(struct {
		Domain    string     `json:"domain"`
		Endpoints []Endpoint `json:"endpoints"`
	}{d.Domain, d.Endpoints})
}

func (d *Domain) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*d = Domain{Domain: s}
		return nil
	}
	var o struct {
		Domain    string     `json:"domain"`
		Endpoints []Endpoint `json:"endpoints"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return fmt.Errorf("allow_domain entry is neither a string nor an object: %w", err)
	}
	*d = Domain{Domain: o.Domain, Endpoints: o.Endpoints}
	return nil
}

// CondPath is a path with an optional platform predicate. nono accepts a bare
// string when there is no predicate, which is the common case.
type CondPath struct {
	Path string
	When []string
}

// P builds an unconditional path entry.
func P(path string) CondPath { return CondPath{Path: path} }

// PWhen builds a path entry guarded by one or more platform predicates.
func PWhen(path string, when ...string) CondPath { return CondPath{Path: path, When: when} }

func (c CondPath) MarshalJSON() ([]byte, error) {
	if len(c.When) == 0 {
		return json.Marshal(c.Path)
	}
	return json.Marshal(struct {
		Path string `json:"path"`
		When any    `json:"when"`
	}{c.Path, whenValue(c.When)})
}

func (c *CondPath) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*c = CondPath{Path: s}
		return nil
	}
	var o struct {
		Path string          `json:"path"`
		When json.RawMessage `json:"when"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return fmt.Errorf("filesystem entry is neither a string nor an object: %w", err)
	}
	w, err := parseWhen(o.When)
	if err != nil {
		return err
	}
	*c = CondPath{Path: o.Path, When: w}
	return nil
}

// CondName is the same union shape over a group name.
type CondName struct {
	Name string
	When []string
}

// G builds an unconditional group reference.
func G(name string) CondName { return CondName{Name: name} }

// GWhen builds a group reference guarded by platform predicates.
func GWhen(name string, when ...string) CondName { return CondName{Name: name, When: when} }

func (c CondName) MarshalJSON() ([]byte, error) {
	if len(c.When) == 0 {
		return json.Marshal(c.Name)
	}
	return json.Marshal(struct {
		Name string `json:"name"`
		When any    `json:"when"`
	}{c.Name, whenValue(c.When)})
}

func (c *CondName) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*c = CondName{Name: s}
		return nil
	}
	var o struct {
		Name string          `json:"name"`
		When json.RawMessage `json:"when"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return fmt.Errorf("group entry is neither a string nor an object: %w", err)
	}
	w, err := parseWhen(o.When)
	if err != nil {
		return err
	}
	*c = CondName{Name: o.Name, When: w}
	return nil
}

// whenValue emits a bare string for a single predicate. nono rejects an empty
// predicate array, which the marshallers avoid by taking the string branch.
func whenValue(w []string) any {
	if len(w) == 1 {
		return w[0]
	}
	return w
}

func parseWhen(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []string{s}, nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("when predicate is neither a string nor an array: %w", err)
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("when predicate array must not be empty")
	}
	return list, nil
}
