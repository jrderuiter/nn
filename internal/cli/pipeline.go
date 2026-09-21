package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/jrderuiter/nn/internal/config"
	"github.com/jrderuiter/nn/internal/nono"
	"github.com/jrderuiter/nn/internal/secrets"
	"github.com/jrderuiter/nn/internal/tool"
	"github.com/jrderuiter/nn/internal/workspace"

	// Providers register themselves.
	_ "github.com/jrderuiter/nn/internal/tool/git"
	_ "github.com/jrderuiter/nn/internal/tool/github"
	_ "github.com/jrderuiter/nn/internal/tool/kubernetes"
	_ "github.com/jrderuiter/nn/internal/tool/runtime"
)

// envKeys are every configuration value the environment may set: nn's own
// keys, plus one per setting of every registered tool.
func envKeys() []config.Key {
	keys := []config.Key{
		{Path: "enable", List: true},
		{Path: "nono.extends", List: true},
		{Path: "nono.groups", List: true},
		{Path: "nono.allow_domain", List: true},
		{Path: "nono.network_profile"},
		{Path: "fnox.binary"},
		{Path: "fnox.config"},
		{Path: "fnox.profile"},
	}
	for _, k := range tool.EnvKeys() {
		keys = append(keys, config.Key{Path: k.Path, List: k.List, Bool: k.Bool})
	}
	return keys
}

// trustsTheProxyCA says whether nono needs to be told to keep a reusable
// interception authority in the system trust store.
//
// It is a macOS question. A Go client such as gh or kubectl reads the macOS
// trust store and ignores the trust bundle variables that nono sets, so
// without the flag it rejects an intercepted connection. Elsewhere Go reads
// SSL_CERT_FILE, which nono already sets, and nono has no such flag to give:
// passing it on Linux fails with "unexpected argument '--trust-proxy-ca'".
var trustsTheProxyCA = runtime.GOOS == "darwin"

// baseAllowVars is the minimal environment that every sandbox keeps. Each
// capability adds the variables its own tools need, which is what a static
// mixin cannot do.
var baseAllowVars = []string{"PATH", "HOME", "USER", "SHELL", "TERM", "LANG", "LC_*", "TMPDIR"}

// options are the inputs that the CLI flags provide.
type options struct {
	configPath string
	only       []string
	skip       []string
	workdir    string
	// skipPreflight builds the profile without checking that the tools can
	// actually work. It lets `nn profile` show the output before fnox or a
	// cluster is set up.
	skipPreflight bool
}

// plan is the fully resolved run, ready to write and launch.
type plan struct {
	cfg        *config.Config
	ws         *workspace.Workspace
	profile    *nono.Profile
	artifacts  []tool.Artifact
	secrets    []tool.Secret
	ensureDirs []string
	extraArgs  []string
	proxyPort  int
	command    []string
}

// prep is everything the pipeline needs before any tool runs.
type prep struct {
	cfg       *config.Config
	ws        *workspace.Workspace
	env       *tool.Env
	providers []tool.Provider
}

// prepare loads the configuration and builds the provider set. It runs no
// preflight, so a caller can check the tools one at a time.
func prepare(opts options) (*prep, error) {
	wd := opts.workdir
	if wd == "" {
		var err error
		if wd, err = os.Getwd(); err != nil {
			return nil, err
		}
	}
	cfg, err := config.Load(config.Options{
		Dir:      wd,
		Explicit: opts.configPath,
		Keys:     envKeys(),
	})
	if err != nil {
		return nil, err
	}
	ws, err := workspace.New(wd)
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	providers, err := selectProviders(cfg, opts)
	if err != nil {
		return nil, err
	}

	return &prep{
		cfg: cfg,
		ws:  ws,
		env: &tool.Env{
			Workdir:     ws.Workdir,
			ArtifactDir: ws.Dir,
			HomeDir:     home,
			Secrets:     secrets.NewResolver(cfg.Fnox.Binary, cfg.Fnox.Config, cfg.Fnox.Profile),
			Lookup:      os.LookupEnv,
		},
		providers: providers,
	}, nil
}

// build runs the whole pipeline up to, but not including, writing files.
//
// It runs no preflight. A run should start the agent, not spend a round trip to
// fnox and the cluster proving that it could. `nn doctor` is where that check
// lives, and a real failure still surfaces, from nono, with its own message.
func build(ctx context.Context, opts options, command []string) (*plan, error) {
	pr, err := prepare(opts)
	if err != nil {
		return nil, err
	}
	cfg, ws, env, providers := pr.cfg, pr.ws, pr.env, pr.providers

	results, err := buildProviders(ctx, providers, env)
	if err != nil {
		return nil, err
	}

	base := baseProfile(cfg)
	m := nono.NewMerger(base)
	var artifacts []tool.Artifact
	var secretRefs []tool.Secret
	var ensure []string
	var extra []string
	for i, r := range results {
		if err := m.Add(r.Fragment, providers[i].Name()); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, r.Artifacts...)
		secretRefs = append(secretRefs, r.Secrets...)
		ensure = append(ensure, r.EnsureDirs...)
		extra = append(extra, r.NonoArgs...)
	}
	// The raw [nono] block applies last, so a hand written rule always wins.
	rawProfile, err := cfg.RawProfile()
	if err != nil {
		return nil, err
	}
	if err := m.AddOverride(rawProfile, "the [nono.profile] block"); err != nil {
		return nil, err
	}

	if n := m.Profile().Network; n != nil && len(n.CustomCredentials) > 0 && trustsTheProxyCA {
		extra = append(extra, "--trust-proxy-ca")
	}
	extra = dedupe(extra)

	return &plan{
		cfg: cfg, ws: ws, profile: m.Profile(), artifacts: artifacts,
		secrets: secretRefs, ensureDirs: ensure, extraArgs: extra, command: command,
	}, nil
}

// selectProviders applies the --tool and --no-tool flags on top of the
// enabled set.
func selectProviders(cfg *config.Config, opts options) ([]tool.Provider, error) {
	var enable []string
	for _, name := range cfg.Enable {
		if !slices.Contains(opts.skip, name) {
			enable = append(enable, name)
		}
	}
	providers, err := tool.Build(cfg.Meta(), enable, cfg.Tools)
	if err != nil {
		return nil, err
	}
	if len(opts.only) == 0 {
		return providers, nil
	}
	keep := map[string]bool{}
	for _, n := range opts.only {
		keep[n] = true
	}
	var out []tool.Provider
	for _, p := range providers {
		if keep[p.Name()] {
			out = append(out, p)
		}
	}
	for n := range keep {
		found := false
		for _, p := range out {
			if p.Name() == n {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("tool %q is not enabled; enable lists %v", n, cfg.Enable)
		}
	}
	return out, nil
}

func buildProviders(ctx context.Context, providers []tool.Provider,
	env *tool.Env) ([]*tool.Result, error) {

	results := make([]*tool.Result, 0, len(providers))
	for _, p := range providers {
		r, err := p.Build(ctx, env)
		if err != nil {
			return nil, fmt.Errorf("tool %q: %w", p.Name(), err)
		}
		results = append(results, r)
	}
	return results, nil
}

// baseProfile is the layer that every run starts from.
func baseProfile(cfg *config.Config) *nono.Profile {
	p := &nono.Profile{
		Schema:  nono.SchemaURL,
		Workdir: &nono.Workdir{Access: "readwrite"},
		// The working directory is already granted, by --allow-cwd together
		// with the access level below, and a directory grant is recursive. The
		// artifact directory is named anyway so that generated files stay
		// writable if someone narrows workdir.access through [nono.profile].
		Filesystem: &nono.Filesystem{
			Allow: []nono.CondPath{nono.P(workspace.ProfileVar)},
		},
		Environment: &nono.Environment{AllowVars: append([]string{}, baseAllowVars...)},
	}

	p.Extends = append(p.Extends, cfg.Nono.Extends...)

	if cfg.Nono.NetworkProfile != "" || len(cfg.Nono.AllowDomain) != 0 {
		p.Network = &nono.Network{NetworkProfile: cfg.Nono.NetworkProfile}
		for _, d := range cfg.Nono.AllowDomain {
			p.Network.AllowDomain = append(p.Network.AllowDomain, nono.Domain{Domain: d})
		}
	}

	for _, g := range cfg.Nono.Groups {
		if p.Groups == nil {
			p.Groups = &nono.Groups{}
		}
		p.Groups.Include = append(p.Groups.Include, nono.G(g))
	}
	return p
}

// write puts the artifacts and the profile on disk.
func (p *plan) write() error {
	if err := p.ws.EnsureGitignore(); err != nil {
		return err
	}
	p.prepareOptionalDirs()
	for _, a := range p.artifacts {
		if err := p.ws.Write(a.RelPath, a.Content, a.Mode); err != nil {
			return fmt.Errorf("write artifact %s: %w", a.RelPath, err)
		}
	}
	body, err := nono.Marshal(p.profile)
	if err != nil {
		return err
	}
	if err := p.ws.Write("profile.json", body, 0o644); err != nil {
		return err
	}
	return nono.Validate(p.ws.ProfilePath())
}

// dedupe keeps the first of each flag, so two tools asking for the same one
// do not pass it twice.
func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// resolveSecrets fetches every declared secret once, before the sandbox
// starts, and returns them as environment entries for the nono process.
//
// This is deliberately the last step before the launch. A backend that asks
// for a touch or a password asks here, while the user is starting the agent,
// not later in the session next to something the agent did.
func (p *plan) resolveSecrets(ctx context.Context) ([]string, error) {
	if len(p.secrets) == 0 {
		return nil, nil
	}
	r := secrets.NewResolver(p.cfg.Fnox.Binary, p.cfg.Fnox.Config, p.cfg.Fnox.Profile)
	// Two routes often want the same key under different names. Fetch each
	// key once, so a backend that asks for a touch asks once.
	byKey := map[string]string{}
	out := make([]string, 0, len(p.secrets))
	for _, s := range p.secrets {
		value, ok := byKey[s.Key]
		if !ok {
			var err error
			if value, err = r.Get(ctx, s.Key); err != nil {
				return nil, err
			}
			byKey[s.Key] = value
		}
		out = append(out, s.EnvVar+"="+value)
	}
	return out, nil
}

// trace prints what nn built, so a failure inside nono can be reproduced by
// hand. It goes to stderr, which keeps it out of a piped profile.
func (p *plan) trace(args []string) {
	fmt.Fprintf(os.Stderr, "nn: profile   %s\n", p.ws.ProfilePath())
	for _, a := range p.artifacts {
		fmt.Fprintf(os.Stderr, "nn: artifact  %s\n", p.ws.Path(a.RelPath))
	}
	fmt.Fprintf(os.Stderr, "nn: WORKDIR   %s\n", p.ws.Workdir)
	for _, s := range p.secrets {
		fmt.Fprintf(os.Stderr, "nn: secret    %s from fnox key %s\n", s.EnvVar, s.Key)
	}
	if a := agentName(p.command); a != "" {
		fmt.Fprintf(os.Stderr, "nn: agent     %s\n", a)
	}
	fmt.Fprintf(os.Stderr, "nn: exec      nono %s\n", strings.Join(quoteArgs(args), " "))
}

// quoteArgs makes the printed command safe to paste back into a shell.
func quoteArgs(in []string) []string {
	out := make([]string, len(in))
	for i, a := range in {
		if strings.ContainsAny(a, " \t\"'\\$") {
			out[i] = strconv.Quote(a)
			continue
		}
		out[i] = a
	}
	return out
}

// validateOnly checks the generated profile without touching the project. It
// writes to a temporary file, because nono validates a path, not a stream.
func (p *plan) validateOnly() error {
	body, err := nono.Marshal(p.profile)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "nn-profile-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return nono.Validate(f.Name())
}

// runArgs is the argv nn hands to nono.
func (p *plan) runArgs() []string {
	return nono.RunArgs{
		ProfilePath: p.ws.ProfilePath(),
		Workdir:     p.ws.Workdir,
		Extra:       p.extraArgs,
		Command:     p.command,
		Banner:      showBanner,
		Diagnostics: showDiagnostics,
	}.Build()
}

// prepareOptionalDirs creates the cache and state directories that capabilities
// asked for, and drops the grant for any directory it cannot prepare.
//
// nono silently ignores a grant whose path does not exist, but it refuses to
// start when a granted path exists and cannot be read. Dropping the grant costs
// one cache directory; keeping it would cost the whole run.
func (p *plan) prepareOptionalDirs() {
	unusable := map[string]bool{}
	for _, d := range p.ensureDirs {
		path := expandHostPath(d, p.ws.Workdir)
		if path == "" {
			continue
		}
		err := os.MkdirAll(path, 0o755)
		if err == nil {
			if _, err = os.Stat(path); err == nil {
				continue
			}
		}
		fmt.Fprintf(os.Stderr, "nn: dropping the grant for %s, which is not usable: %v\n", d, err)
		unusable[d] = true
	}
	if len(unusable) == 0 || p.profile.Filesystem == nil {
		return
	}
	keep := p.profile.Filesystem.Allow[:0]
	for _, c := range p.profile.Filesystem.Allow {
		if !unusable[c.Path] {
			keep = append(keep, c)
		}
	}
	p.profile.Filesystem.Allow = keep
}

// expandHostPath resolves the profile-side variables that nn itself has to
// understand. nono expands the rest at launch.
func expandHostPath(in, workdir string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		cache = filepath.Join(home, ".cache")
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(home, ".local", "state")
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	r := strings.NewReplacer(
		"$HOME", home,
		"$WORKDIR", workdir,
		"$XDG_CACHE_HOME", cache,
		"$XDG_STATE_HOME", state,
		"$XDG_DATA_HOME", data,
	)
	out := r.Replace(in)
	if strings.Contains(out, "$") {
		// An unexpanded variable means nn would create the wrong directory.
		return ""
	}
	return out
}

var errNoCommand = errors.New("no command given; use nn -- <command> [args...]")
