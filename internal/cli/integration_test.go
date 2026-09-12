package cli_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// syscall.Exec replaces the calling process, so the exec path cannot be
// exercised in-process. These tests run the real nn binary as a subprocess
// against stand-in nono/wrapper binaries.

var binDir string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "nn-itest")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	binDir = dir

	for _, pkg := range []string{
		"github.com/jderuiter/nn/cmd/nn",
		"github.com/jderuiter/nn/internal/testbin/fakenono",
		"github.com/jderuiter/nn/internal/testbin/fakewrapper",
	} {
		name := filepath.Base(pkg)
		cmd := exec.Command("go", "build", "-o", filepath.Join(dir, name), pkg)
		if out, err := cmd.CombinedOutput(); err != nil {
			panic(string(out))
		}
	}
	os.Exit(m.Run())
}

type record struct {
	Exe   string            `json:"exe"`
	Argv  []string          `json:"argv"`
	Env   map[string]string `json:"env"`
	Stdin string            `json:"stdin"`
}

type result struct {
	stdout, stderr string
	code           int
}

// project writes a config plus the profile it references, and returns a deep
// subdirectory to run from, so every test also exercises discovery via the git
// root.
func project(t *testing.T, cfg string) string {
	t.Helper()
	deep, _ := projectDirs(t, cfg)
	return deep
}

// projectDirs also returns the .nono directory, for tests that need to add or
// remove files in it.
func projectDirs(t *testing.T, cfg string) (deep, nonoDir string) {
	t.Helper()
	root := projectRoot(t, cfg)
	return filepath.Join(root, "cmd", "server"), filepath.Join(root, ".nono")
}

// projectRoot builds the tree and returns its top. The .git marker is what
// makes the config reachable from the deep subdirectory: discovery looks in the
// current directory and the git root, and nowhere else.
func projectRoot(t *testing.T, cfg string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nonoDir := filepath.Join(root, ".nono")
	if err := os.MkdirAll(nonoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonoDir, "nn.yml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	// nn checks that a path-valued profile exists, so the fixture needs one.
	if err := os.WriteFile(filepath.Join(nonoDir, "profile.json"), []byte(`{"meta":{"name":"t"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cmd", "server"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func runNN(t *testing.T, dir string, env []string, stdin string, args ...string) result {
	t.Helper()
	cmd := exec.Command(filepath.Join(binDir, "nn"), args...)
	cmd.Dir = dir
	cmd.Env = append([]string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
	}, env...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if err != nil {
		if asExit(err, &ee) {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run: %v", err)
		}
	}
	return result{out.String(), errb.String(), code}
}

func asExit(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*target = e
	}
	return ok
}

func decode(t *testing.T, r result) record {
	t.Helper()
	var rec record
	if err := json.Unmarshal([]byte(r.stdout), &rec); err != nil {
		t.Fatalf("decode %q (stderr: %s): %v", r.stdout, r.stderr, err)
	}
	return rec
}

const baseCfg = "nono_bin: fakenono\ncommand: [claude]\nprofile: profile.json\nallow_cwd: true\n"

func TestExecPassesArgvThrough(t *testing.T) {
	dir := project(t, baseCfg)
	rec := decode(t, runNN(t, dir, nil, ""))

	joined := strings.Join(rec.Argv, " ")
	for _, want := range []string{"run", "--allow-cwd", "--profile", "-- claude"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q: %s", want, joined)
		}
	}
	// Anchored against the .nono dir, and absolute: nono runs from a
	// subdirectory here.
	if !strings.Contains(joined, filepath.Join(".nono", "profile.json")) {
		t.Errorf("profile should be anchored to .nono: %s", joined)
	}
}

func TestExitCodePropagates(t *testing.T) {
	dir := project(t, baseCfg)
	r := runNN(t, dir, []string{"FAKE_EXIT=42"}, "")
	if r.code != 42 {
		t.Errorf("exit = %d, want 42 (stderr: %s)", r.code, r.stderr)
	}
}

func TestStdinIsInherited(t *testing.T) {
	dir := project(t, baseCfg)
	r := runNN(t, dir, []string{"FAKE_READ_STDIN=1"}, "hello from the terminal")
	if got := decode(t, r).Stdin; got != "hello from the terminal" {
		t.Errorf("stdin = %q", got)
	}
}

func TestWrapperChainSurvivesTwoExecHops(t *testing.T) {
	cfg := baseCfg + "wrappers:\n  - [fakewrapper, exec, --]\n"
	dir := project(t, cfg)
	r := runNN(t, dir, []string{"FAKE_ENV_REPORT=FAKE_WRAPPER_RAN"}, "")
	rec := decode(t, r)
	if rec.Env["FAKE_WRAPPER_RAN"] != "1" {
		t.Errorf("wrapper did not run: %+v", rec.Env)
	}
	if rec.Argv[0] == "" || !strings.Contains(strings.Join(rec.Argv, " "), "run") {
		t.Errorf("argv after wrapper: %v", rec.Argv)
	}
}

// The only end-to-end proof that syscall.Exec's third argument is wired up.
func TestEnvReachesTheChild(t *testing.T) {
	cfg := baseCfg + "env:\n  NN_TEST_SET: yes\nenv_unset: [NN_TEST_DROP]\n"
	dir := project(t, cfg)
	r := runNN(t, dir, []string{
		"FAKE_ENV_REPORT=NN_TEST_SET,NN_TEST_DROP,NN_TEST_KEEP",
		"NN_TEST_DROP=should-be-gone",
		"NN_TEST_KEEP=inherited",
	}, "")
	rec := decode(t, r)
	if rec.Env["NN_TEST_SET"] != "yes" {
		t.Errorf("env: override did not arrive: %+v", rec.Env)
	}
	if _, ok := rec.Env["NN_TEST_DROP"]; ok {
		t.Errorf("env_unset did not take effect: %+v", rec.Env)
	}
	if rec.Env["NN_TEST_KEEP"] != "inherited" {
		t.Errorf("unrelated inherited var was lost: %+v", rec.Env)
	}
}

// Regression test for the subtlest bug in the design: exec.LookPath reads nn's
// own PATH, so a config that sets env.PATH must still resolve the chain
// against the PATH it is about to exec with.
func TestPathOverrideAffectsResolution(t *testing.T) {
	other := t.TempDir()
	src, err := os.ReadFile(filepath.Join(binDir, "fakenono"))
	if err != nil {
		t.Fatal(err)
	}
	// A second fakenono, identifiable by the marker it reports.
	if err := os.WriteFile(filepath.Join(other, "fakenono"), src, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := baseCfg + "env:\n  PATH: \"" + other + ":${PATH}\"\n  NN_WHICH: other\n"
	dir := project(t, cfg)
	rec := decode(t, runNN(t, dir, []string{"FAKE_ENV_REPORT=NN_WHICH,PATH"}, ""))

	if !strings.HasPrefix(rec.Env["PATH"], other) {
		t.Errorf("PATH override did not apply: %q", rec.Env["PATH"])
	}
	// Exe is the file that actually ran, and it must be the copy on the
	// overridden PATH rather than the one on nn's own.
	exe, err := filepath.EvalSymlinks(rec.Exe)
	if err != nil {
		t.Fatal(err)
	}
	wantDir, err := filepath.EvalSymlinks(other)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(exe) != wantDir {
		t.Errorf("ran %q, want the copy in %s — LookPath used the wrong PATH", exe, wantDir)
	}
}

func TestMissingBinaryExits127(t *testing.T) {
	dir := project(t, "nono_bin: definitely-not-installed\ncommand: [x]\n")
	r := runNN(t, dir, nil, "")
	if r.code != 127 {
		t.Errorf("exit = %d, want 127", r.code)
	}
	if !strings.Contains(r.stderr, "not found on PATH") {
		t.Errorf("stderr = %q", r.stderr)
	}
}

func TestConfigErrorExits2(t *testing.T) {
	dir := project(t, "command: [x]\nallow_domain: [example.com]\n")
	r := runNN(t, dir, nil, "")
	if r.code != 2 {
		t.Errorf("exit = %d, want 2", r.code)
	}
	if !strings.Contains(r.stderr, "network.allow_domain") {
		t.Errorf("stderr should point at the profile: %q", r.stderr)
	}
}

func TestCommandOverrideFromCLI(t *testing.T) {
	dir := project(t, baseCfg)
	rec := decode(t, runNN(t, dir, nil, "", "--", "go", "test", "-v"))
	joined := strings.Join(rec.Argv, " ")
	if !strings.HasSuffix(joined, "-- go test -v") {
		t.Errorf("argv = %s", joined)
	}
	if strings.Contains(joined, "claude") {
		t.Errorf("-- args should replace the command: %s", joined)
	}
}

// A path-valued profile that does not exist would otherwise be handed to
// nono, which reports it from two processes down with no idea which config
// produced it.
func TestMissingProfileIsCaughtByNN(t *testing.T) {
	dir, nonoDir := projectDirs(t, "nono_bin: fakenono\nprofile: profile.json\ncommand: [x]\n")
	if err := os.Remove(filepath.Join(nonoDir, "profile.json")); err != nil {
		t.Fatal(err)
	}
	r := runNN(t, dir, nil, "", "print")
	if r.code != 2 {
		t.Errorf("exit = %d, want 2", r.code)
	}
	if !strings.Contains(r.stderr, "profile.json does not exist") {
		t.Errorf("stderr = %q", r.stderr)
	}
}

// The common near-miss: the profile is there, under another extension.
func TestMissingProfileSuggestsNearbyFile(t *testing.T) {
	dir, nonoDir := projectDirs(t, "nono_bin: fakenono\nprofile: profile.json\ncommand: [x]\n")
	if err := os.Remove(filepath.Join(nonoDir, "profile.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonoDir, "profile.jsonc"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runNN(t, dir, nil, "", "print")
	if !strings.Contains(r.stderr, `did you mean "profile.jsonc"`) {
		t.Errorf("stderr should suggest the near-miss:\n%s", r.stderr)
	}
}

// A bare profile name is nono's to resolve, so nn must not check the filesystem.
// A bare nn.yml at the top of the project is the second supported layout. Its
// own directory anchors the profile, and is the project root for workdir.
func TestBareConfigAtProjectRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "nono_bin: fakenono\ncommand: [claude]\nprofile: profile.json\nworkdir: .\n"
	if err := os.WriteFile(filepath.Join(root, "nn.yml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "profile.json"), []byte(`{"meta":{"name":"t"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "cmd", "server")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	r := runNN(t, deep, nil, "", "print")
	if r.code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", r.code, r.stderr)
	}
	// Resolved for the symlinked temp dir, as everywhere else.
	real, _ := filepath.EvalSymlinks(root)
	for _, want := range []string{
		"--profile " + filepath.Join(real, "profile.json"),
		"--workdir " + real,
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("stdout should contain %q:\n%s", want, r.stdout)
		}
	}
}

// A bare nn.yml in the current directory wins over the project's .nono one.
func TestBareConfigBeatsNonoDirEndToEnd(t *testing.T) {
	root := projectRoot(t, baseCfg)
	if err := os.WriteFile(filepath.Join(root, "nn.yml"),
		[]byte("nono_bin: fakenono\ncommand: [bare]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runNN(t, filepath.Join(root, "cmd", "server"), nil, "", "print")
	if r.code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "-- bare") {
		t.Errorf("bare nn.yml should win:\n%s", r.stdout)
	}
}

func TestProfileNameIsNotFileChecked(t *testing.T) {
	dir := project(t, "nono_bin: fakenono\nprofile: go-dev\ncommand: [x]\n")
	r := runNN(t, dir, nil, "", "print")
	if r.code != 0 {
		t.Errorf("exit = %d, want 0 (stderr: %s)", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "--profile go-dev") {
		t.Errorf("stdout = %q", r.stdout)
	}
}
