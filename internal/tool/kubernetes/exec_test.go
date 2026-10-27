package kubernetes

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeExecKubeconfig writes a kubeconfig whose one context runs plugin, a
// shell script that the test supplies, and returns its path.
func writeExecKubeconfig(t *testing.T, plugin, extra string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plugin.sh"), []byte("#!/bin/sh\n"+plugin), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `apiVersion: v1
kind: Config
current-context: gke
clusters:
  - name: gke
    cluster:
      server: https://34.1.2.3
      certificate-authority-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCmZha2UKLS0tLS1FTkQgQ0VSVElGSUNBVEUtLS0tLQo=
contexts:
  - name: gke
    context:
      cluster: gke
      user: gke-user
users:
  - name: gke-user
    user:
      exec:
        apiVersion: client.authentication.k8s.io/v1beta1
        command: ./plugin.sh
        args: ["--flag"]
        env:
          - name: PLUGIN_MODE
            value: test
` + extra
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const credentialJSON = `{"apiVersion":"client.authentication.k8s.io/v1beta1","kind":"ExecCredential","status":{"token":"%s"%s}}`

// A relative command resolves against the kubeconfig, as kubectl does, and
// the plugin sees its own env entries and its arguments.
func TestExecTokenRunsThePlugin(t *testing.T) {
	script := fmt.Sprintf(`[ "$1" = --flag ] && [ "$PLUGIN_MODE" = test ] || exit 3
printf '%s'
`, fmt.Sprintf(credentialJSON, "real-token", ""))
	path := writeExecKubeconfig(t, script, "")
	got, err := execToken(context.Background(), path, "gke", os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if got != "real-token" {
		t.Fatalf("got token %q", got)
	}
}

// A plugin that asks for the cluster reads it from KUBERNETES_EXEC_INFO, and
// nono gives it no terminal.
func TestExecTokenProvidesClusterInfo(t *testing.T) {
	script := fmt.Sprintf(`case "$KUBERNETES_EXEC_INFO" in
*'"server":"https://34.1.2.3"'*'"interactive":false'*) ;;
*) echo "$KUBERNETES_EXEC_INFO" >&2; exit 3 ;;
esac
printf '%s'
`, fmt.Sprintf(credentialJSON, "t", ""))
	path := writeExecKubeconfig(t, script, "        provideClusterInfo: true\n")
	if _, err := execToken(context.Background(), path, "gke", os.Environ()); err != nil {
		t.Fatal(err)
	}
}

func TestExecTokenRejectsBadOutput(t *testing.T) {
	for name, tc := range map[string]struct{ script, want string }{
		"failure": {"echo 'login required' >&2; exit 1", "login required"},
		"no json": {"echo hello", "printed no ExecCredential"},
		"no token": {fmt.Sprintf("printf '%s'", `{"status":{"clientCertificateData":"x"}}`),
			"a client certificate cannot go through the proxy"},
		"expired": {fmt.Sprintf("printf '%s'", fmt.Sprintf(credentialJSON, "t", `,"expirationTimestamp":"2001-01-01T00:00:00Z"`)),
			"expired"},
	} {
		t.Run(name, func(t *testing.T) {
			path := writeExecKubeconfig(t, tc.script, "")
			_, err := execToken(context.Background(), path, "gke", os.Environ())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error with %q, got: %v", tc.want, err)
			}
		})
	}
}

// nono runs the capture without a terminal, so a plugin that always prompts
// can never succeed.
func TestExecRejectsAnInteractivePlugin(t *testing.T) {
	path := writeExecKubeconfig(t, "exit 0", "        interactiveMode: Always\n")
	_, err := execToken(context.Background(), path, "gke", os.Environ())
	if err == nil || !strings.Contains(err.Error(), "needs a terminal") {
		t.Fatalf("got: %v", err)
	}
}

func TestCredentialPrintsOnlyTheToken(t *testing.T) {
	path := writeExecKubeconfig(t, fmt.Sprintf("printf '%s'", fmt.Sprintf(credentialJSON, "real-token", "")), "")
	var out bytes.Buffer
	if err := credential(context.Background(), []string{"--kubeconfig", path, "--context", "gke"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "real-token" {
		t.Fatalf("got %q", out.String())
	}
	if err := credential(context.Background(), []string{"--context", "gke"}, &out); err == nil {
		t.Fatal("a missing --kubeconfig must be an error")
	}
}

// The token stays on the host: the sandbox kubeconfig holds the phantom
// token, and the capture runs nn on the host to call the plugin.
func TestExecBuildsAProxyRoute(t *testing.T) {
	path := writeExecKubeconfig(t, "exit 0", "")
	p, err := newProvider(t, fmt.Sprintf("auth = \"exec\"\nkubeconfig = %q\n", path))
	if err != nil {
		t.Fatal(err)
	}
	e := testEnv()
	e.Executable = "/usr/local/bin/nn"
	res, err := p.Build(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	capture := res.Fragment.CredentialCapture[routeName]
	want := []string{"/usr/local/bin/nn", "credential", "kubernetes", "--kubeconfig", path, "--context", "gke"}
	if strings.Join(capture.Command, " ") != strings.Join(want, " ") {
		t.Fatalf("got capture %v, want %v", capture.Command, want)
	}
	if capture.CacheTTLSecs != 60 {
		t.Fatalf("got cache ttl %d", capture.CacheTTLSecs)
	}
	route := res.Fragment.Network.CustomCredentials[routeName]
	if route.Upstream != "https://34.1.2.3" || route.CredentialKey != "cmd://"+routeName {
		t.Fatalf("got route %+v", route)
	}
	for _, a := range res.Artifacts {
		if bytes.Contains(a.Content, []byte("plugin.sh")) {
			t.Fatalf("the plugin reached the sandbox in %s", a.RelPath)
		}
	}
}

func TestExecNeedsAnExecPlugin(t *testing.T) {
	p, err := newProvider(t, fmt.Sprintf("auth = \"exec\"\nkubeconfig = %q\ncontext = \"local\"\n", mustAbs(t, fixture)))
	if err != nil {
		t.Fatal(err)
	}
	err = p.resolveAll(testEnv())
	if err == nil || !strings.Contains(err.Error(), "has no exec plugin") {
		t.Fatalf("got: %v", err)
	}
}

func TestExecRejectsAServiceAccount(t *testing.T) {
	if _, err := newFromTOML(t, "auth = \"exec\"\nservice_account = \"ro\"\n"); err == nil {
		t.Fatal("auth = exec together with service_account must be an error")
	}
}

// The host form cannot run a plugin in the sandbox, so its error points at the
// form that can.
func TestHostFormPointsAtExec(t *testing.T) {
	p, err := newProvider(t, fmt.Sprintf("auth = \"host\"\nkubeconfig = %q\ncontext = \"prod-eks\"\n", mustAbs(t, fixture)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Build(context.Background(), testEnv())
	if err == nil || !strings.Contains(err.Error(), `auth = "exec"`) {
		t.Fatalf("got: %v", err)
	}
}
