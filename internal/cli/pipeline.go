package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/jrderuiter/nn/internal/config"
	"github.com/jrderuiter/nn/internal/nono"
	"github.com/jrderuiter/nn/internal/secrets"
	"github.com/jrderuiter/nn/internal/tool"
	"github.com/jrderuiter/nn/internal/workspace"

	// Providers register themselves.
	_ "github.com/jrderuiter/nn/internal/tool/azuredevops"
	_ "github.com/jrderuiter/nn/internal/tool/git"
	_ "github.com/jrderuiter/nn/internal/tool/github"
	_ "github.com/jrderuiter/nn/internal/tool/kubernetes"
	_ "github.com/jrderuiter/nn/internal/tool/runtime"
)

// envKeys are every configuration value the environment may set: nn's own
// keys, plus one per setting of every registered tool.
func envKeys() []config.Key {
	keys := []config.Key{
		{Path: "nono.extends", List: true},
		{Path: "nono.groups", List: true},
		{Path: "nono.allow_domain", List: true},
		{Path: "nono.network_profile"},
		{Path: "fnox.binary"},
		{Path: "fnox.config"},
		{Path: "fnox.profile"},
	}
	for _, k := range tool.EnvKeys() {
		keys = append(keys, config.Key{Path: k.Path, List: k.List, Bool: k.Bool, Enable: k.Enable})
	}
	return keys
}

// trustsTheProxyCA says whether nono needs to be told to keep a reusable
// interception authority in the system trust store. An empty goos means the
// platform nn runs on.
//
// It is a macOS question. A Go client such as gh or kubectl reads the macOS
// trust store and ignores the trust bundle variables that nono sets, so
// without the flag it rejects an intercepted connection. Elsewhere Go reads
// SSL_CERT_FILE, which nono already sets, and nono has no such flag to give:
// passing it on Linux fails with "unexpected argument '--trust-proxy-ca'".
func trustsTheProxyCA(goos string) bool {
	if goos == "" {
		goos = runtime.GOOS
	}
	return goos == "darwin"
}

// baseAllowVars is the minimal environment that every sandbox keeps. Each
// tool adds the variables its own programs need, which is what a static
// mixin cannot do.
var baseAllowVars = []string{"PATH", "HOME", "USER", "SHELL", "TERM", "LANG", "LC_*", "TMPDIR"}

// options are the inputs of one invocation. The CLI flags fill most of them.
// Each command gets its own copy, so nothing is shared between two runs or two
// tests.
type options struct {
	configPath string
	only       []string
	// mixin leaves out the base layer and the [nono] settings, so the
	// profile holds only what the selected tools add.
	mixin   bool
	workdir string
	// agent names the [agents.<name>] section to apply, whatever the command.
	agent string

	// The flags of nn run.
	dryRun      bool
	verbose     bool
	banner      bool
	diagnostics bool

	// stderr receives warnings and the trace. Nil means os.Stderr.
	stderr io.Writer
	// gitRemotes lists the remote URLs of the repository in a directory. Nil
	// means ask git. A test hands in a fixture, because a case directory
	// cannot hold a real .git directory.
	gitRemotes func(ctx context.Context, dir string) ([]string, error)
	// gitCommonDir finds the shared git directory of a linked worktree. Nil
	// means ask git.
	gitCommonDir func(ctx context.Context, dir string) (string, error)
	// goos is the platform that decides the trust flag. Empty means this one.
	goos string
	// getenv reads the host environment for the settings that are not tool
	// configuration. Nil means os.Getenv.
	getenv func(string) string
}

// inHerdr says whether nn runs in a herdr pane.
func (o options) inHerdr() bool {
	getenv := o.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	return getenv("HERDR_ENV") == "1"
}

// warnf writes one line to the warning stream.
func (o options) warnf(format string, a ...any) {
	w := o.stderr
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, "nn: "+format+"\n", a...)
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
	opts       options
	// agent is the [agents.<name>] section that the profile applies, or an
	// empty string when it applies none.
	agent string
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
	remotes := opts.gitRemotes
	if remotes == nil {
		remotes = gitRemotes
	}
	commonDir := opts.gitCommonDir
	if commonDir == nil {
		commonDir = gitCommonDir
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
			GitRemotes: func(ctx context.Context) ([]string, error) {
				return remotes(ctx, ws.Workdir)
			},
			GitCommonDir: func(ctx context.Context) (string, error) {
				return commonDir(ctx, ws.Workdir)
			},
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

	agent, err := selectAgent(cfg, opts.agent, command)
	if err != nil {
		return nil, err
	}
	if name := missingAgentSection(cfg, opts.agent, command); name != "" {
		opts.warnf("%s has no [agents.%s] section in nn.toml, so it runs without an agent pack", name, name)
	}
	// A mixin leaves out the [nono] settings, so it leaves out the agent too.
	if opts.mixin {
		agent = ""
	}

	results, err := buildProviders(ctx, providers, env)
	if err != nil {
		return nil, err
	}

	section := cfg.Nono
	if agent != "" {
		section = section.WithAgent(cfg.Agents[agent])
	}
	base := baseProfile(section)
	if opts.mixin {
		base = &nono.Profile{Schema: nono.SchemaURL}
	}
	m := nono.NewMerger(base)
	var artifacts []tool.Artifact
	var secretRefs []tool.Secret
	var ensure []string
	var extra []string
	var gitCfg []tool.GitConfig
	var gitCfgFrom []string
	for i, r := range results {
		if err := m.Add(r.Fragment, providers[i].Name()); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, r.Artifacts...)
		secretRefs = append(secretRefs, r.Secrets...)
		ensure = append(ensure, r.EnsureDirs...)
		extra = append(extra, r.NonoArgs...)
		if len(r.GitConfig) > 0 {
			gitCfg = append(gitCfg, r.GitConfig...)
			gitCfgFrom = append(gitCfgFrom, providers[i].Name())
		}
	}
	if len(gitCfg) > 0 {
		if err := m.Add(gitConfigFragment(gitCfg), strings.Join(gitCfgFrom, ", ")); err != nil {
			return nil, err
		}
	}
	if opts.mixin {
		if err := keepArtifactGrant(m); err != nil {
			return nil, err
		}
	}
	// The agent block merges like a tool, so a clash with a tool is reported
	// rather than decided.
	if agent != "" {
		agentProfile, err := cfg.AgentProfile(agent)
		if err != nil {
			return nil, err
		}
		if agentProfile != nil {
			if err := m.Add(agentProfile, "the [agents."+agent+".profile] block"); err != nil {
				return nil, err
			}
		}
	}
	// The raw [nono] block applies last, so a hand written rule always wins.
	if !opts.mixin {
		rawProfile, err := cfg.RawProfile()
		if err != nil {
			return nil, err
		}
		if err := m.AddOverride(rawProfile, "the [nono.profile] block"); err != nil {
			return nil, err
		}
	}

	if opts.inHerdr() {
		allowHerdrPane(m.Profile())
	}

	if n := m.Profile().Network; n != nil && len(n.CustomCredentials) > 0 && trustsTheProxyCA(opts.goos) {
		extra = append(extra, "--trust-proxy-ca")
	}
	extra = dedupe(extra)

	return &plan{
		cfg: cfg, ws: ws, profile: m.Profile(), artifacts: artifacts,
		secrets: secretRefs, ensureDirs: ensure, extraArgs: extra, command: command,
		opts: opts, agent: agent,
	}, nil
}

// allowHerdrPane lets HERDR_PANE_ID into the sandbox, so a hook inside it can
// name its pane.
//
// It only extends a list that exists. Without one, nono passes every variable
// already, and a list of only this name would strip all the others. It uses
// allow_vars rather than set_vars, because the profile file is shared by every
// pane in the project. HERDR_ENV and HERDR_SOCKET_PATH stay out: with them,
// herdr's own hook reaches for the herdr socket, which is its full API.
func allowHerdrPane(p *nono.Profile) {
	if p.Environment == nil || len(p.Environment.AllowVars) == 0 {
		return
	}
	for _, v := range p.Environment.AllowVars {
		if v == "HERDR_PANE_ID" {
			return
		}
	}
	p.Environment.AllowVars = append(p.Environment.AllowVars, "HERDR_PANE_ID")
}

// selectAgent picks the [agents.<name>] section for a run. The --agent flag
// wins. Without it, the base name of the command selects the section, so
// ~/.local/bin/claude still finds [agents.claude]. A command with no section,
// such as kubectl, gets the shared profile only.
//
// A named agent without a section is an error, because a typo would otherwise
// start the command without its pack, and nothing would say so.
func selectAgent(cfg *config.Config, flag string, command []string) (string, error) {
	// `--agent -- agy` hands the separator to the flag as its value.
	if strings.HasPrefix(flag, "-") {
		return "", fmt.Errorf("--agent needs a name, as in --agent claude; got %q", flag)
	}
	if flag != "" {
		if _, ok := cfg.Agents[flag]; !ok {
			if len(cfg.Agents) == 0 {
				return "", fmt.Errorf("agent %q has no [agents.%s] section; nn.toml has no agent sections", flag, flag)
			}
			return "", fmt.Errorf("agent %q has no [agents.%s] section in nn.toml; it has %v",
				flag, flag, agentNames(cfg))
		}
		return flag, nil
	}
	if len(command) == 0 {
		return "", nil
	}
	base := filepath.Base(command[0])
	if _, ok := cfg.Agents[base]; ok {
		return base, nil
	}
	return "", nil
}

// missingAgentSection names a known agent that runs without a section of its
// own, or returns an empty string. It is a warning and not an error: a project
// that still puts the pack in [nono] extends works as it did before.
func missingAgentSection(cfg *config.Config, flag string, command []string) string {
	if flag != "" {
		return ""
	}
	name := agentName(command)
	if name == "" {
		return ""
	}
	if _, ok := cfg.Agents[name]; ok {
		return ""
	}
	return name
}

// agentNames lists the agent sections that the configuration declares.
func agentNames(cfg *config.Config) []string {
	out := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// keepArtifactGrant puts back the one base grant that a mixin still needs.
//
// A tool can point at the artifact directory without writing a file there, for
// example GH_CONFIG_DIR, so the check reads the profile rather than the
// artifact list. Without the grant, a profile that extends the mixin and
// narrows workdir access cannot reach the generated kubeconfig.
func keepArtifactGrant(m *nono.Merger) error {
	body, err := nono.Marshal(m.Profile())
	if err != nil {
		return err
	}
	if !strings.Contains(string(body), workspace.ProfileVar+"/") {
		return nil
	}
	return m.Add(&nono.Profile{
		Filesystem: &nono.Filesystem{Allow: []nono.CondPath{nono.P(workspace.ProfileVar)}},
	}, "the generated files")
}

// gitConfigFragment numbers the git configuration entries of every tool in
// one list. It uses the GIT_CONFIG_COUNT form rather than a config file, so
// nothing is written and the host git configuration is untouched. That form
// needs git 2.31 or newer.
func gitConfigFragment(entries []tool.GitConfig) *nono.Profile {
	vars := map[string]string{"GIT_CONFIG_COUNT": strconv.Itoa(len(entries))}
	for i, e := range entries {
		vars["GIT_CONFIG_KEY_"+strconv.Itoa(i)] = e.Key
		vars["GIT_CONFIG_VALUE_"+strconv.Itoa(i)] = e.Value
	}
	return &nono.Profile{Environment: &nono.Environment{SetVars: vars}}
}

// gitRemotes lists the remote URLs of the repository in dir. It asks git
// rather than reading .git/config, because git also follows worktrees and
// included files. A missing git or a directory that is not a repository is not
// an error: the tools that use remotes then have nothing to derive.
func gitRemotes(ctx context.Context, dir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "config", "--get-regexp", `^remote\..*\.url$`)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, nil
	}
	var urls []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if _, url, ok := strings.Cut(line, " "); ok {
			urls = append(urls, url)
		}
	}
	return urls, nil
}

// gitCommonDir returns the shared git directory when dir is in a linked
// worktree. There, .git is a file that points into the main repository, which
// keeps the objects, refs and configuration outside the working directory. In
// a normal clone the two directories are the same, and it returns nothing. A
// missing git or a directory that is not a repository is not an error.
func gitCommonDir(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--path-format=absolute",
		"--git-dir", "--git-common-dir")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 || lines[0] == lines[1] {
		return "", nil
	}
	return lines[1], nil
}

// selectProviders applies the --tool flag on top of the configured set.
func selectProviders(cfg *config.Config, opts options) ([]tool.Provider, error) {
	providers, err := tool.Build(cfg.Meta(), cfg.Tools)
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
	// Walk the flags, not the set, so the first unknown name is always the
	// one reported.
	for _, n := range opts.only {
		found := false
		for _, p := range out {
			if p.Name() == n {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("tool %q is not configured in nn.toml; it has %v",
				n, configuredNames(cfg))
		}
	}
	return out, nil
}

// configuredNames lists the tools the configuration actually declares.
func configuredNames(cfg *config.Config) []string {
	out := make([]string, 0, len(cfg.Tools))
	for name := range cfg.Tools {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
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

// baseProfile is the layer that every run starts from. It takes the [nono]
// section with the agent section of the run already applied.
func baseProfile(n config.Nono) *nono.Profile {
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
		// On Linux, nono's defaults let a process write /tmp but not read it,
		// so a build that reads back its own temporary files fails. macOS
		// already grants the read. The predicate keeps the profile the same
		// bytes on both platforms.
		Groups: &nono.Groups{Include: []nono.CondName{nono.GWhen("linux_temp_read", "linux")}},
		// On Linux, a socket lets the agent make a program outside the sandbox
		// act for it, so a socket needs a grant unless the user turns this off.
		// nono applies the key only on Linux, so the profile stays the same
		// bytes on every platform.
		Linux: &nono.Linux{AfUnixMediation: "pathname"},
	}

	p.Extends = append(p.Extends, n.Extends...)

	if n.NetworkProfile != "" || len(n.AllowDomain) != 0 {
		p.Network = &nono.Network{NetworkProfile: n.NetworkProfile}
		for _, d := range n.AllowDomain {
			p.Network.AllowDomain = append(p.Network.AllowDomain, nono.Domain{Domain: d})
		}
	}

	for _, g := range n.Groups {
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
	if err := p.ws.Write(workspace.ProfileFile(p.agent), body, 0o644); err != nil {
		return err
	}
	removed, err := p.ws.Prune(usedEntries(p.artifacts, body))
	if err != nil {
		return fmt.Errorf("remove stale files: %w", err)
	}
	for _, name := range removed {
		p.opts.warnf("removed %s, which no configured tool uses", p.ws.Path(name))
	}
	return nono.Validate(p.ws.ProfilePath(p.agent))
}

// artifactRef matches a path under the artifact directory in a profile, and
// captures its first segment.
var artifactRef = regexp.MustCompile(regexp.QuoteMeta(workspace.ProfileVar+"/") + `([^/"]+)`)

// usedEntries names the top level entries of the artifact directory that the
// run uses. An artifact claims its first segment. So does a path that the
// profile names, because a tool can point a program at a directory there
// without writing a file, as GH_CONFIG_DIR does.
func usedEntries(artifacts []tool.Artifact, profile []byte) map[string]bool {
	used := map[string]bool{}
	for _, a := range artifacts {
		first, _, _ := strings.Cut(filepath.ToSlash(a.RelPath), "/")
		used[first] = true
	}
	for _, m := range artifactRef.FindAllSubmatch(profile, -1) {
		used[string(m[1])] = true
	}
	return used
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
		if s.Format != "" {
			value = strings.ReplaceAll(s.Format, "{}", value)
		}
		out = append(out, s.EnvVar+"="+value)
	}
	return out, nil
}

// trace prints what nn built, so a failure inside nono can be reproduced by
// hand. It goes to stderr, which keeps it out of a piped profile.
func (p *plan) trace(args []string) {
	o := p.opts
	o.warnf("profile   %s", p.ws.ProfilePath(p.agent))
	for _, a := range p.artifacts {
		o.warnf("artifact  %s", p.ws.Path(a.RelPath))
	}
	o.warnf("WORKDIR   %s", p.ws.Workdir)
	for _, s := range p.secrets {
		o.warnf("secret    %s from fnox key %s", s.EnvVar, s.Key)
	}
	if a := agentName(p.command); a != "" {
		o.warnf("agent     %s", a)
	}
	o.warnf("exec      nono %s", strings.Join(quoteArgs(args), " "))
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
		ProfilePath: p.ws.ProfilePath(p.agent),
		Workdir:     p.ws.Workdir,
		Extra:       p.extraArgs,
		Command:     p.command,
		Banner:      p.opts.banner,
		Diagnostics: p.opts.diagnostics,
	}.Build()
}

// prepareOptionalDirs creates the cache and state directories that tools
// asked for, and drops the grant for any directory it cannot prepare.
//
// nono silently ignores a grant whose path does not exist, but it refuses to
// start when a granted path exists and cannot be read. Dropping the grant costs
// one cache directory; keeping it would cost the whole run.
//
// It runs when nn writes the profile, not when nn builds it, because creating
// a directory is a change to the host. `nn profile` and `nn doctor` promise to
// change nothing, so their profile can hold a grant that `nn run` drops.
func (p *plan) prepareOptionalDirs() {
	dropGrants(p.profile, makeOptionalDirs(p.opts, p.ensureDirs, p.ws.Workdir))
}

// makeOptionalDirs creates each directory and returns the ones it could not.
func makeOptionalDirs(o options, dirs []string, workdir string) map[string]bool {
	unusable := map[string]bool{}
	for _, d := range dirs {
		path := expandHostPath(d, workdir)
		if path == "" {
			continue
		}
		err := os.MkdirAll(path, 0o755)
		if err == nil {
			if _, err = os.Stat(path); err == nil {
				continue
			}
		}
		o.warnf("dropping the grant for %s, which is not usable: %v", d, err)
		unusable[d] = true
	}
	return unusable
}

// dropGrants removes the filesystem grants for the given profile paths.
func dropGrants(profile *nono.Profile, paths map[string]bool) {
	if len(paths) == 0 || profile.Filesystem == nil {
		return
	}
	keep := profile.Filesystem.Allow[:0]
	for _, c := range profile.Filesystem.Allow {
		if !paths[c.Path] {
			keep = append(keep, c)
		}
	}
	profile.Filesystem.Allow = keep
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

var errNoCommand = errors.New("no command given; use nn run -- <command> [args...]")
