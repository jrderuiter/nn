package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindWalksUp(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "nn.toml"), "[nono]\nnetwork_profile = \"claude-code\"\n")
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Options{Dir: deep, Explicit: ""})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Nono.NetworkProfile != "claude-code" {
		t.Fatalf("expected the parent configuration to apply, got %q", cfg.Nono.NetworkProfile)
	}
}

// A nested repository must not inherit its parent's tools, because that
// would silently widen the sandbox of an unrelated project.
func TestFindStopsAtARepositoryBoundary(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "nn.toml"), "[nono]\nnetwork_profile = \"claude-code\"\n")
	inner := filepath.Join(root, "vendor", "other")
	write(t, filepath.Join(inner, ".git", "HEAD"), "ref: refs/heads/main\n")
	cfg, err := Load(Options{Dir: inner, Explicit: ""})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Nono.NetworkProfile != "" {
		t.Fatalf("a nested repository must not pick up the parent configuration, got %q", cfg.Nono.NetworkProfile)
	}
}

func TestMissingConfigurationIsNotAnError(t *testing.T) {
	cfg, err := Load(Options{Dir: t.TempDir(), Explicit: ""})
	if err != nil {
		t.Fatalf("running with no configuration must work: %v", err)
	}
	if len(cfg.Sources()) != 0 {
		t.Fatalf("expected no sources, got %v", cfg.Sources())
	}
}

func TestUnknownTopLevelKeyIsAnError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nn.toml")
	write(t, path, "agnet = \"claude\"\n")
	_, err := Load(Options{Dir: root, Explicit: path})
	if err == nil {
		t.Fatal("a misspelled key must be an error, not a silent default")
	}
	if !strings.Contains(err.Error(), "agnet") {
		t.Fatalf("the error should name the key, got: %v", err)
	}
}

func TestToolTablesAreLeftToTheProviders(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nn.toml")
	write(t, path, "[tools.github]\nrepos = [\"a/b\"]\n")
	cfg, err := Load(Options{Dir: root, Explicit: path})
	if err != nil {
		t.Fatalf("a tool table must not count as an unknown key: %v", err)
	}
	if _, ok := cfg.Tools["github"]; !ok {
		t.Fatal("the github table should be captured for lazy decoding")
	}
}

func TestExplicitPathSkipsTheSearch(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "nn.toml"), "[nono]\nnetwork_profile = \"claude-code\"\n")
	other := filepath.Join(root, "other.toml")
	write(t, other, "[nono]\nnetwork_profile = \"developer\"\n")
	cfg, err := Load(Options{Dir: root, Explicit: other})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Nono.NetworkProfile != "developer" {
		t.Fatalf("the explicit file must win, got %q", cfg.Nono.NetworkProfile)
	}
}

var testKeys = []Key{
	{Path: "tools.mise", Enable: true},
	{Path: "tools.kubernetes", Enable: true},
	{Path: "tools.go", Enable: true},
	{Path: "tools.github", Enable: true},

	{Path: "nono.extends", List: true},
	{Path: "nono.network_profile"},
	{Path: "tools.kubernetes.context"},
	{Path: "tools.kubernetes.token_ttl"},
	{Path: "tools.kubernetes.in_cluster", Bool: true},
	{Path: "tools.github.secret"},
}

func TestEnvSetsAValue(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "nn.toml"), "[nono]\nnetwork_profile = \"claude-code\"\n")
	t.Setenv("NN_NONO_NETWORK_PROFILE", "developer")
	cfg, err := Load(Options{Dir: root, Keys: testKeys})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Nono.NetworkProfile != "developer" {
		t.Fatalf("the environment must win over the file, got %q", cfg.Nono.NetworkProfile)
	}
}

// A key whose own name contains an underscore would be ambiguous if the
// variable name were parsed. Matching known keys instead settles it.
func TestEnvHandlesAnUnderscoreInTheKey(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NN_NONO_NETWORK_PROFILE", "developer")
	cfg, err := Load(Options{Dir: root, Keys: testKeys})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Nono.NetworkProfile != "developer" {
		t.Fatalf("got %q", cfg.Nono.NetworkProfile)
	}
}

func TestEnvSetsAToolValue(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "nn.toml"), "[tools.kubernetes]\n")
	t.Setenv("NN_TOOLS_KUBERNETES_CONTEXT", "prod")
	t.Setenv("NN_TOOLS_KUBERNETES_TOKEN_TTL", "30m")
	cfg, err := Load(Options{Dir: root, Keys: testKeys})
	if err != nil {
		t.Fatal(err)
	}
	var k struct {
		Context  string `toml:"context"`
		TokenTTL string `toml:"token_ttl"`
	}
	md := cfg.Meta()
	if err := md.PrimitiveDecode(cfg.Tools["kubernetes"], &k); err != nil {
		t.Fatal(err)
	}
	if k.Context != "prod" || k.TokenTTL != "30m" {
		t.Fatalf("got %+v", k)
	}
}

// A setting alone must not turn a tool on. An image can then carry defaults
// for a tool that the project does not use.
func TestEnvSettingDoesNotEnableATool(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NN_TOOLS_KUBERNETES_CONTEXT", "prod")
	cfg, err := Load(Options{Dir: root, Keys: testKeys})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Tools["kubernetes"]; ok {
		t.Fatal("a setting variable should not have enabled the tool")
	}
}

// The section variable comes before the settings, so the settings fill in a
// tool that the environment alone turns on.
func TestEnvEnableThenSetting(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NN_TOOLS_KUBERNETES", "true")
	t.Setenv("NN_TOOLS_KUBERNETES_CONTEXT", "prod")
	cfg, err := Load(Options{Dir: root, Keys: testKeys})
	if err != nil {
		t.Fatal(err)
	}
	var k struct {
		Context string `toml:"context"`
	}
	md := cfg.Meta()
	if err := md.PrimitiveDecode(cfg.Tools["kubernetes"], &k); err != nil {
		t.Fatal(err)
	}
	if k.Context != "prod" {
		t.Fatalf("got %+v", k)
	}
}

// A false section variable wins over the settings of the same tool.
func TestEnvDisableWinsOverSettings(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "nn.toml"), "[tools.kubernetes]\n")
	t.Setenv("NN_TOOLS_KUBERNETES", "false")
	t.Setenv("NN_TOOLS_KUBERNETES_CONTEXT", "prod")
	cfg, err := Load(Options{Dir: root, Keys: testKeys})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Tools["kubernetes"]; ok {
		t.Fatal("a false value should have removed the tool")
	}
}

// A list comes through as a comma separated string.
func TestEnvListValue(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NN_NONO_EXTENDS", "jr/clean_env, jr/mise")
	cfg, err := Load(Options{Dir: root, Keys: testKeys})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Nono.Extends) != 2 || cfg.Nono.Extends[1] != "jr/mise" {
		t.Fatalf("got %v", cfg.Nono.Extends)
	}
}

// A runtime has no settings, so the section variable is the only way to turn
// one on from the environment.
func TestEnvEnablesAToolWithoutSettings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NN_TOOLS_MISE", "true")
	t.Setenv("NN_TOOLS_GO", "1")
	cfg, err := Load(Options{Dir: root, Keys: testKeys})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mise", "go"} {
		if _, ok := cfg.Tools[name]; !ok {
			t.Errorf("NN_TOOLS_%s should have enabled %q", strings.ToUpper(name), name)
		}
	}
}

// A false value turns a tool off, which is how a project default is dropped
// for one run.
func TestEnvDisablesATool(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "nn.toml"), "[tools.mise]\n")
	t.Setenv("NN_TOOLS_MISE", "false")
	cfg, err := Load(Options{Dir: root, Keys: testKeys})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Tools["mise"]; ok {
		t.Fatal("a false value should have removed the tool")
	}
}

// Turning a tool on must not wipe the settings its section already carries.
func TestEnvEnableKeepsExistingSettings(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "nn.toml"), "[tools.github]\nsecret = \"MY_TOKEN\"\n")
	t.Setenv("NN_TOOLS_GITHUB", "true")
	cfg, err := Load(Options{Dir: root, Keys: testKeys})
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Secret string `toml:"secret"`
	}
	md := cfg.Meta()
	if err := md.PrimitiveDecode(cfg.Tools["github"], &g); err != nil {
		t.Fatal(err)
	}
	if g.Secret != "MY_TOKEN" {
		t.Fatalf("the enable variable overwrote the section, got %q", g.Secret)
	}
}

// A later layer cannot delete a table, so enabled = false is how it drops a
// tool that an earlier layer declares.
func TestEnabledFalseRemovesATool(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	write(t, filepath.Join(root, "xdg", "nn", "config.toml"), "[tools.github]\nsecret = \"MY_TOKEN\"\n")
	write(t, filepath.Join(root, "project", "nn.toml"), "[tools.github]\nenabled = false\n\n[tools.mise]\n")
	cfg, err := Load(Options{Dir: filepath.Join(root, "project")})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Tools["github"]; ok {
		t.Fatal("enabled = false should have removed the tool")
	}
	if _, ok := cfg.Tools["mise"]; !ok {
		t.Fatal("a tool without the switch must stay")
	}
}

// The same switch drops one named entry of a tool, such as a cluster that
// this machine cannot reach, and leaves the rest of the tool alone.
func TestEnabledFalseRemovesANamedEntry(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "nn.toml"), "[tools.kubernetes]\ntoken_ttl = \"30m\"\n\n"+
		"[tools.kubernetes.clusters.dev]\nauth = \"host\"\n\n"+
		"[tools.kubernetes.clusters.prod]\nauth = \"host\"\nenabled = false\n")
	cfg, err := Load(Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	var k map[string]any
	md := cfg.Meta()
	if err := md.PrimitiveDecode(cfg.Tools["kubernetes"], &k); err != nil {
		t.Fatal(err)
	}
	clusters, _ := k["clusters"].(map[string]any)
	if _, ok := clusters["prod"]; ok {
		t.Fatalf("enabled = false should have removed the cluster, got %v", clusters)
	}
	if _, ok := clusters["dev"]; !ok || k["token_ttl"] != "30m" {
		t.Fatalf("the rest of the tool must stay, got %v", k)
	}
}

// A provider never sees the switch, so no tool has to accept the key.
func TestEnabledTrueIsDropped(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "nn.toml"), "[tools.github]\nenabled = true\nsecret = \"MY_TOKEN\"\n")
	cfg, err := Load(Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	var g map[string]any
	md := cfg.Meta()
	if err := md.PrimitiveDecode(cfg.Tools["github"], &g); err != nil {
		t.Fatal(err)
	}
	if _, ok := g[EnabledKey]; ok || g["secret"] != "MY_TOKEN" {
		t.Fatalf("got %v", g)
	}
}

func TestEnabledMustBeABool(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "nn.toml"), "[tools.github]\nenabled = \"no\"\n")
	_, err := Load(Options{Dir: root})
	if err == nil || !strings.Contains(err.Error(), "tools.github.enabled") {
		t.Fatalf("got %v", err)
	}
}

// The environment applies last, so a true value turns back on a tool that a
// file switched off, with the settings that the file wrote.
func TestEnvEnableOverridesEnabledFalse(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "nn.toml"), "[tools.github]\nsecret = \"MY_TOKEN\"\nenabled = false\n")
	t.Setenv("NN_TOOLS_GITHUB", "true")
	cfg, err := Load(Options{Dir: root, Keys: testKeys})
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Secret string `toml:"secret"`
	}
	md := cfg.Meta()
	if err := md.PrimitiveDecode(cfg.Tools["github"], &g); err != nil {
		t.Fatal(err)
	}
	if g.Secret != "MY_TOKEN" {
		t.Fatalf("the variable should have turned the tool back on, got %q", g.Secret)
	}
}

// The variable a user would guess from enabled = true is not read, so it must
// fail rather than leave the tool as it was.
func TestEnvEnabledVariableIsAnError(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NN_TOOLS_GITHUB_ENABLED", "true")
	_, err := Load(Options{Dir: root, Keys: testKeys})
	if err == nil || !strings.Contains(err.Error(), "NN_TOOLS_GITHUB_ENABLED") {
		t.Fatalf("got %v", err)
	}
}

func TestEnvLeavesUnsetKeysAlone(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "nn.toml"), "[nono]\nnetwork_profile = \"claude-code\"\n")
	cfg, err := Load(Options{Dir: root, Keys: testKeys})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Nono.NetworkProfile != "claude-code" {
		t.Fatalf("got %q", cfg.Nono.NetworkProfile)
	}
}

func TestAgentSectionsLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nn.toml")
	write(t, path, "[nono]\nextends = [\"default\"]\nnetwork_profile = \"minimal\"\n\n"+
		"[agents.claude]\nextends = [\"nolabs-ai/claude\"]\nnetwork_profile = \"claude-code\"\n\n"+
		"[agents.agy]\nallow_domain = [\"cloudcode-pa.googleapis.com\"]\n")
	cfg, err := Load(Options{Dir: dir, Explicit: path})
	if err != nil {
		t.Fatal(err)
	}
	claude := cfg.Nono.WithAgent(cfg.Agents["claude"])
	if strings.Join(claude.Extends, ",") != "default,nolabs-ai/claude" || claude.NetworkProfile != "claude-code" {
		t.Fatalf("got %+v", claude)
	}
	// An agent without a network profile keeps the one from [nono].
	agy := cfg.Nono.WithAgent(cfg.Agents["agy"])
	if agy.NetworkProfile != "minimal" || len(agy.AllowDomain) != 1 {
		t.Fatalf("got %+v", agy)
	}
	// Applying a section must not change the shared one.
	if len(cfg.Nono.Extends) != 1 || cfg.Nono.NetworkProfile != "minimal" {
		t.Fatalf("the [nono] section changed: %+v", cfg.Nono)
	}
}

func TestAgentSectionRejectsUnknownKeysAndBadNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nn.toml")
	write(t, path, "[agents.claude]\nextend = [\"nolabs-ai/claude\"]\n")
	if _, err := Load(Options{Dir: dir, Explicit: path}); err == nil || !strings.Contains(err.Error(), "agents.claude.extend") {
		t.Fatalf("a misspelled key must be an error, got %v", err)
	}
	// The name becomes part of a file name.
	write(t, path, "[agents.\"../x\"]\n")
	if _, err := Load(Options{Dir: dir, Explicit: path}); err == nil {
		t.Fatal("an agent name that is not safe in a file name must be an error")
	}
}

// An agent profile block decodes like [nono.profile], and a misspelled key in
// it fails when it is decoded rather than being dropped.
func TestAgentProfileBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nn.toml")
	write(t, path, "[agents.agy.profile.network]\nopen_port_range = [[49152, 65535]]\n")
	cfg, err := Load(Options{Dir: dir, Explicit: path})
	if err != nil {
		t.Fatal(err)
	}
	p, err := cfg.AgentProfile("agy")
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || len(p.Network.OpenPortRange) != 1 || p.Network.OpenPortRange[0] != [2]int{49152, 65535} {
		t.Fatalf("got %+v", p)
	}

	write(t, path, "[agents.agy.profile.network]\nopen_ports = [1]\n")
	if cfg, err = Load(Options{Dir: dir, Explicit: path}); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.AgentProfile("agy"); err == nil {
		t.Fatal("a misspelled key in an agent profile block must be an error")
	}
}

// The linux table decodes in both raw profile blocks. Without a Linux field
// the strict decoder rejected it as an unknown field.
func TestProfileBlockDecodesLinux(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nn.toml")
	write(t, path, "[nono.profile.linux]\naf_unix_mediation = \"pathname\"\n\n"+
		"[agents.claude.profile.linux]\naf_unix_mediation = \"off\"\n")
	cfg, err := Load(Options{Dir: dir, Explicit: path})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := cfg.RawProfile()
	if err != nil {
		t.Fatal(err)
	}
	if raw == nil || raw.Linux == nil || raw.Linux.AfUnixMediation != "pathname" {
		t.Fatalf("[nono.profile.linux]: got %+v", raw)
	}
	agent, err := cfg.AgentProfile("claude")
	if err != nil {
		t.Fatal(err)
	}
	if agent == nil || agent.Linux == nil || agent.Linux.AfUnixMediation != "off" {
		t.Fatalf("[agents.claude.profile.linux]: got %+v", agent)
	}
}

// The machine local layer exists so that a committed nn.toml can hold what is
// true everywhere. It adds to the table rather than replacing it, or a local
// kubectl path would drop the tool's real settings.
func TestLocalFileMergesIntoTheProjectFile(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "nn.toml"),
		"[nono]\nnetwork_profile = \"claude-code\"\n\n[tools.kubernetes]\nservice_account = \"ro\"\n")
	write(t, filepath.Join(root, LocalFileName),
		"[tools.kubernetes]\nkubectl = \"/opt/homebrew/bin/kubectl\"\n")

	cfg, err := Load(Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	var k struct {
		ServiceAccount string `toml:"service_account"`
		Kubectl        string `toml:"kubectl"`
	}
	md := cfg.Meta()
	if err := md.PrimitiveDecode(cfg.Tools["kubernetes"], &k); err != nil {
		t.Fatal(err)
	}
	if k.ServiceAccount != "ro" {
		t.Fatalf("the committed settings must survive, got %+v", k)
	}
	if k.Kubectl != "/opt/homebrew/bin/kubectl" {
		t.Fatalf("the local file must add its own key, got %+v", k)
	}
	if cfg.Nono.NetworkProfile != "claude-code" {
		t.Fatalf("an untouched section must survive, got %q", cfg.Nono.NetworkProfile)
	}
	if len(cfg.Sources()) != 2 {
		t.Fatalf("both files must be reported, got %v", cfg.Sources())
	}
}

func TestLocalFileWinsOverTheProjectFile(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "nn.toml"), "[tools.kubernetes]\ncontext = \"shared\"\n")
	write(t, filepath.Join(root, LocalFileName), "[tools.kubernetes]\ncontext = \"mine\"\n")

	cfg, err := Load(Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	var k struct {
		Context string `toml:"context"`
	}
	md := cfg.Meta()
	if err := md.PrimitiveDecode(cfg.Tools["kubernetes"], &k); err != nil {
		t.Fatal(err)
	}
	if k.Context != "mine" {
		t.Fatalf("got %q, want mine", k.Context)
	}
}

// A local file on its own means nothing: it belongs to the project file that
// was found, and is not searched for separately.
func TestLocalFileIsNotReadWithoutAProjectFile(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, LocalFileName), "[nono]\nnetwork_profile = \"developer\"\n")
	cfg, err := Load(Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Nono.NetworkProfile != "" {
		t.Fatalf("got %q, want nothing", cfg.Nono.NetworkProfile)
	}
}

// The environment gives every value as a string. A bool key has to become a
// bool before the merged configuration is decoded, or the decode fails.
func TestEnvSetsABoolValue(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NN_TOOLS_KUBERNETES_IN_CLUSTER", "true")
	cfg, err := Load(Options{Dir: root, Keys: testKeys})
	if err != nil {
		t.Fatal(err)
	}
	var k struct {
		InCluster bool `toml:"in_cluster"`
	}
	md := cfg.Meta()
	if err := md.PrimitiveDecode(cfg.Tools["kubernetes"], &k); err != nil {
		t.Fatal(err)
	}
	if !k.InCluster {
		t.Fatal("the variable must turn the setting on")
	}
}

func TestEnvUnsetsABoolValue(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "nn.toml"), "[tools.kubernetes]\nin_cluster = true\n")
	t.Setenv("NN_TOOLS_KUBERNETES_IN_CLUSTER", "false")
	cfg, err := Load(Options{Dir: root, Keys: testKeys})
	if err != nil {
		t.Fatal(err)
	}
	var k struct {
		InCluster bool `toml:"in_cluster"`
	}
	md := cfg.Meta()
	if err := md.PrimitiveDecode(cfg.Tools["kubernetes"], &k); err != nil {
		t.Fatal(err)
	}
	if k.InCluster {
		t.Fatal("a false value must turn the setting off")
	}
}
