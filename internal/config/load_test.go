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

// A nested repository must not inherit its parent's capabilities, because that
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
	{Path: "tools.enabled", List: true},
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

func TestEnvSetsAToolValueAndCreatesTheSection(t *testing.T) {
	root := t.TempDir()
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
	prim, ok := cfg.Tools["kubernetes"]
	if !ok {
		t.Fatal("the environment should have created the tool section")
	}
	md := cfg.Meta()
	if err := md.PrimitiveDecode(prim, &k); err != nil {
		t.Fatal(err)
	}
	if k.Context != "prod" || k.TokenTTL != "30m" {
		t.Fatalf("got %+v", k)
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

// tools.enabled is a list like any other, so the environment replaces the
// one in the file rather than adding to it. A pod spec states the whole set.
func TestEnvReplacesTheEnableList(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "nn.toml"), "[tools]\nenabled = [\"mise\", \"git\"]\n")
	t.Setenv("NN_TOOLS_ENABLED", "git, kubernetes")
	cfg, err := Load(Options{Dir: root, Keys: testKeys})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.Enabled, ",") != "git,kubernetes" {
		t.Fatalf("got %v", cfg.Enabled)
	}
}

// A section configures a tool and nothing more. It is captured for its
// provider, but it does not put the tool in tools.enabled.
func TestASectionDoesNotEnableATool(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "nn.toml"), "[tools.github]\nsecret = \"MY_TOKEN\"\n")
	cfg, err := Load(Options{Dir: root, Keys: testKeys})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Tools["github"]; !ok {
		t.Fatal("the section should still be captured for its provider")
	}
	if len(cfg.Enabled) != 0 {
		t.Fatalf("a section must not enable its tool, got %v", cfg.Enabled)
	}
}

// The list shares [tools] with the tool tables. Load takes it out, so it is
// never mistaken for a tool, and a value that is not a list is reported.
func TestEnabledIsTakenOutOfTheToolTables(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "nn.toml"), "[tools]\nenabled = [\"git\"]\n\n[tools.git]\nname = \"Jane\"\n")
	cfg, err := Load(Options{Dir: root, Keys: testKeys})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Tools["enabled"]; ok {
		t.Error("the list must not stay among the tool tables")
	}
	if _, ok := cfg.Tools["git"]; !ok || len(cfg.Enabled) != 1 {
		t.Errorf("got tables %v and enabled %v", cfg.Tools, cfg.Enabled)
	}

	write(t, filepath.Join(root, "nn.toml"), "[tools]\nenabled = \"git\"\n")
	if _, err := Load(Options{Dir: root, Keys: testKeys}); err == nil || !strings.Contains(err.Error(), "list") {
		t.Errorf("a single name instead of a list must be an error, got %v", err)
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
