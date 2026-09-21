// Package tool defines the provider contract and the registry.
//
// A tool is one thing an agent can be given: a runtime, git, GitHub, a
// Kubernetes cluster. Its provider turns one typed section of nn.toml into a
// nono profile fragment plus the files that fragment refers to. Providers never
// invoke nono.
package tool

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/jrderuiter/nn/internal/nono"
	"github.com/jrderuiter/nn/internal/secrets"
)

// Env is everything a provider may read about the run.
type Env struct {
	Workdir     string // absolute working directory
	ArtifactDir string // absolute path of $WORKDIR/.nono/nn
	HomeDir     string
	Secrets     *secrets.Resolver
	Lookup      func(string) (string, bool) // host environment, for preflight only
}

// Artifact is a file that the generated profile refers to.
type Artifact struct {
	RelPath string // relative to Env.ArtifactDir
	Mode    os.FileMode
	Content []byte
}

// Secret is a value that the launcher resolves once, before the sandbox
// starts, and puts in the environment of the nono process.
//
// Resolving up front matters. A secret backend that asks for a touch or a
// password then asks while the user is starting the agent, which is a moment
// they can judge. A lazy fetch would ask in the middle of a session, next to
// whatever the agent was doing, and teach the user to approve on demand.
type Secret struct {
	// EnvVar is the name nono reads with the env:// scheme.
	EnvVar string
	// Key is the fnox key that holds the value.
	Key string
}

// Result is what a provider contributes to the run.
type Result struct {
	Fragment  *nono.Profile
	Artifacts []Artifact
	NonoArgs  []string
	// EnsureDirs are host directories to create before launch. nono silently
	// drops a grant whose path does not exist, which would leave a cache
	// directory unwritable inside the sandbox. Entries may use $HOME and the
	// XDG variables.
	EnsureDirs []string
	// Secrets are resolved at launch and handed to nono in its environment.
	// They never reach the sandbox: the profile lists no such name in
	// allow_vars, and the proxy gives the child a phantom token instead.
	Secrets []Secret
}

// Provider is one tool.
type Provider interface {
	// Name is the key under [tools] in nn.toml.
	Name() string
	// Preflight makes sure that the tool can work, before nn writes anything
	// or launches nono.
	Preflight(ctx context.Context, e *Env) error
	// Build returns the fragment and its artifacts.
	Build(ctx context.Context, e *Env) (*Result, error)
}

// Factory decodes a tool's own sub-table of nn.toml.
type Factory func(md toml.MetaData, prim toml.Primitive) (Provider, error)

type entry struct {
	factory Factory
	// proto returns a pointer to a zero value of the capability's own
	// configuration struct, with its defaults applied. The toml tags on that
	// struct are what `nn capability` turns into flags.
	proto func() any
}

var registry = map[string]entry{}

// order fixes the sequence in which providers run, so a generated profile is
// byte stable across runs and golden tests stay meaningful. Tools come first
// because they grant the ground a command runs on, and anything not listed runs
// after these, in name order.
var order = []string{
	"mise", "go", "node", "bun", "python", "rust", "java", "nix",
	"git", "github", "kubernetes",
}

// Register adds a tool. proto returns a pointer to the tool's own
// configuration struct with defaults applied.
func Register(name string, f Factory, proto func() any) {
	if _, dup := registry[name]; dup {
		panic("tool registered twice: " + name)
	}
	registry[name] = entry{factory: f, proto: proto}
}

// Prototype returns a fresh configuration struct for a tool.
func Prototype(name string) (any, bool) {
	e, ok := registry[name]
	if !ok || e.proto == nil {
		return nil, false
	}
	return e.proto(), true
}

// EnvKeys lists the configuration paths each tool exposes, so the environment
// can set them. They come from the toml tags of the tool's own configuration
// struct, which keeps the two in step.
func EnvKeys() []ConfigKey {
	var out []ConfigKey
	for _, name := range Known() {
		proto, ok := Prototype(name)
		if !ok {
			continue
		}
		t := reflect.TypeOf(proto)
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			key := strings.Split(f.Tag.Get("toml"), ",")[0]
			if key == "" || key == "-" || !f.IsExported() {
				continue
			}
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			out = append(out, ConfigKey{
				Path: "tools." + name + "." + key,
				List: ft.Kind() == reflect.Slice,
				Bool: ft.Kind() == reflect.Bool,
			})
		}
	}
	return out
}

// ConfigKey is one configuration value a tool exposes.
type ConfigKey struct {
	Path string
	List bool
	Bool bool
}

// Runtimes lists the tools that grant a language runtime or version manager.
// They are the ones with no settings at all.
func Runtimes() []string {
	var out []string
	for _, name := range Known() {
		proto, ok := Prototype(name)
		if !ok {
			continue
		}
		t := reflect.TypeOf(proto)
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.NumField() == 0 {
			out = append(out, name)
		}
	}
	return out
}

// Known lists every registered tool name, sorted.
func Known() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// buildOrder puts the enabled tools in a fixed sequence: the ones named in
// order first, then anything else by name, so output stays stable as tools are
// added. The sequence in enabled_tools does not matter.
func buildOrder(enabled map[string]bool) []string {
	var out []string
	for _, name := range order {
		if enabled[name] {
			out = append(out, name)
		}
	}
	var rest []string
	for name := range enabled {
		if !slices.Contains(order, name) {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// Build turns the enabled tools into providers, in registry order. Each one
// decodes its own table, and a tool enabled with no table runs on its defaults.
//
// An unknown name is an error in either place. In enabled_tools, ignoring it
// would start the agent with less access than the configuration asked for. As
// a table, it is almost always a misspelling, and its settings would never
// apply.
func Build(md toml.MetaData, enable []string, tables map[string]toml.Primitive) ([]Provider, error) {
	for name := range tables {
		if _, ok := registry[name]; !ok {
			return nil, fmt.Errorf("unknown tool [tools.%s]; known tools are %v", name, Known())
		}
	}
	enabled := map[string]bool{}
	for _, name := range enable {
		if _, ok := registry[name]; !ok {
			return nil, fmt.Errorf("enabled_tools: unknown tool %q; known tools are %v", name, Known())
		}
		enabled[name] = true
	}
	var out []Provider
	for _, name := range buildOrder(enabled) {
		p, err := registry[name].factory(md, tables[name])
		if err != nil {
			return nil, fmt.Errorf("tool %q: %w", name, err)
		}
		out = append(out, p)
	}
	return out, nil
}
