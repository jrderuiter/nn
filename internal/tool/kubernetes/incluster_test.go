package kubernetes

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeToken builds an unsigned token whose subject names an account, which is
// the only part of a service account token that nn reads.
func fakeToken(namespace, name string) string {
	claims := `{"sub":"system:serviceaccount:` + namespace + `:` + name + `","aud":["https://kubernetes.default.svc"]}`
	return "eyJhbGciOiJSUzI1NiJ9." +
		base64.RawURLEncoding.EncodeToString([]byte(claims)) +
		".not-a-signature"
}

// writePodDir lays out the three files the kubelet mounts.
func writePodDir(t *testing.T, namespace, account string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"token":     fakeToken(namespace, account),
		"ca.crt":    "-----BEGIN CERTIFICATE-----\nnotreal\n-----END CERTIFICATE-----\n",
		"namespace": namespace,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestReadPodIdentity(t *testing.T) {
	dir := writePodDir(t, "apps", "agent")
	id, err := readPodIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if id.Namespace != "apps" {
		t.Fatalf("namespace: got %q, want apps", id.Namespace)
	}
	if id.ServiceAccount != "agent" {
		t.Fatalf("service account: got %q, want agent", id.ServiceAccount)
	}
	if id.TokenFile != filepath.Join(dir, "token") {
		t.Fatalf("token file: got %q", id.TokenFile)
	}
	if id.CAFile != filepath.Join(dir, "ca.crt") {
		t.Fatalf("ca file: got %q", id.CAFile)
	}
}

// The error has to say that this is not a pod, because the likely cause is
// auth = "in_cluster" set on a laptop.
func TestReadPodIdentityWithoutATokenSaysSo(t *testing.T) {
	_, err := readPodIdentity(t.TempDir())
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "not a pod") {
		t.Fatalf("the error must name the cause, got: %v", err)
	}
}

func TestReadPodIdentityRejectsAnEmptyToken(t *testing.T) {
	dir := writePodDir(t, "apps", "agent")
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := readPodIdentity(dir)
	if err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("want an empty token error, got: %v", err)
	}
}

func TestReadPodIdentityNeedsTheCertificateAuthority(t *testing.T) {
	dir := writePodDir(t, "apps", "agent")
	if err := os.Remove(filepath.Join(dir, "ca.crt")); err != nil {
		t.Fatal(err)
	}
	_, err := readPodIdentity(dir)
	if err == nil || !strings.Contains(err.Error(), "ca.crt") {
		t.Fatalf("want a certificate authority error, got: %v", err)
	}
}

func TestServiceAccountFromToken(t *testing.T) {
	cases := []struct {
		name  string
		token string
		want  string
	}{
		{"a real subject", fakeToken("kube-system", "deployer"), "deployer"},
		{"not a token at all", "hunter2", ""},
		{"a token with no subject", "a." + base64.RawURLEncoding.EncodeToString([]byte(`{}`)) + ".c", ""},
		{"a subject that is not an account", "a." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"alice"}`)) + ".c", ""},
		{"a payload that is not base64", "a.!!!.c", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := serviceAccountFromToken([]byte(c.token)); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestLooksLikePod(t *testing.T) {
	dir := writePodDir(t, "apps", "agent")
	inPod := func(k string) (string, bool) {
		if k == "KUBERNETES_SERVICE_HOST" {
			return "10.96.0.1", true
		}
		return "", false
	}
	nowhere := func(string) (string, bool) { return "", false }

	if !looksLikePod(inPod, dir) {
		t.Fatal("the variable and the token together mean a pod")
	}
	if looksLikePod(nowhere, dir) {
		t.Fatal("a mounted directory alone is not a pod")
	}
	if looksLikePod(inPod, t.TempDir()) {
		t.Fatal("the variable alone is not a pod")
	}
	if looksLikePod(nil, dir) {
		t.Fatal("no environment to read means no pod")
	}
}
