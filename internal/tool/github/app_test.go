package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/jrderuiter/nn/internal/tool"
)

func newProvider(t *testing.T, body string) (tool.Provider, error) {
	t.Helper()
	var cfg struct {
		Tools map[string]toml.Primitive `toml:"tools"`
	}
	md, err := toml.Decode("[tools.github]\n"+body, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	return New(md, cfg.Tools["github"])
}

func appEnv(remotes ...string) *tool.Env {
	return &tool.Env{
		Workdir: "/w", ArtifactDir: "/w/.nono/nn", Executable: "/bin/nn",
		Secrets:    fakeSecrets{"GITHUB_APP_PRIVATE_KEY": true},
		GitRemotes: func(context.Context) ([]string, error) { return remotes, nil },
	}
}

func buildApp(t *testing.T, body string, remotes ...string) *tool.Result {
	t.Helper()
	p, err := newProvider(t, body)
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Build(context.Background(), appEnv(remotes...))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Every route of an App gets a capture, so nono asks for a new token before
// the old one expires.
func TestAppRoutesUseCaptures(t *testing.T) {
	r := buildApp(t, "app_id = \"42\"\ninstallation_id = \"7\"\nrepositories = [\"nn\"]\n")
	for _, name := range []string{"github", "github_git"} {
		route := r.Fragment.Network.CustomCredentials[name]
		if route.CredentialKey != "cmd://"+name {
			t.Errorf("route %s reads %q", name, route.CredentialKey)
		}
		c, ok := r.Fragment.CredentialCapture[name]
		if !ok {
			t.Fatalf("route %s has no capture", name)
		}
		want := []string{"/bin/nn", "credential", "github", "--app-id", "42", "--installation-id", "7", "--repository", "nn"}
		if name == "github_git" {
			want = append(want, "--git")
		}
		if !slices.Equal(c.Command, want) {
			t.Errorf("route %s runs %v, want %v", name, c.Command, want)
		}
		if c.CacheTTLSecs <= 0 || c.CacheTTLSecs >= 3600 {
			t.Errorf("route %s caches for %d seconds, which outlives the token", name, c.CacheTTLSecs)
		}
	}
}

// The private key is the only secret, so fnox is asked once, at launch.
func TestAppResolvesOnlyThePrivateKey(t *testing.T) {
	r := buildApp(t, "app_id = \"42\"\ninstallation_id = \"7\"\n")
	if len(r.Secrets) != 1 || r.Secrets[0] != (tool.Secret{EnvVar: appKeyVar, Key: "GITHUB_APP_PRIVATE_KEY"}) {
		t.Fatalf("got %+v", r.Secrets)
	}
}

func TestAppFindsTheRepositoryFromTheRemote(t *testing.T) {
	for _, remote := range []string{
		"https://github.com/acme/widget.git",
		"git@github.com:acme/widget.git",
		"ssh://git@github.com/acme/widget",
	} {
		c := buildApp(t, "app_id = \"42\"\n", remote, "https://dev.azure.com/x/y/_git/z").Fragment.CredentialCapture["github"]
		if !slices.Contains(c.Command, "--repo") || !slices.Contains(c.Command, "acme/widget") {
			t.Errorf("%s: got %v", remote, c.Command)
		}
	}
}

// A fork has the App on one side only, so nn does not guess.
func TestAppRejectsAmbiguousRemotes(t *testing.T) {
	p, err := newProvider(t, "app_id = \"42\"\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, remotes := range [][]string{
		nil,
		{"https://github.com/me/widget", "https://github.com/acme/widget"},
	} {
		if err := p.Preflight(context.Background(), appEnv(remotes...)); err == nil || !strings.Contains(err.Error(), "installation_id") {
			t.Errorf("%v: got %v", remotes, err)
		}
	}
}

func TestAppSettingsNeedAppID(t *testing.T) {
	for _, body := range []string{"installation_id = \"7\"\n", "repositories = [\"nn\"]\n"} {
		if _, err := newProvider(t, body); err == nil {
			t.Errorf("%q should need app_id", body)
		}
	}
}

// fakeGitHub answers the two App endpoints and records what it was sent.
type fakeGitHub struct {
	pub  *rsa.PublicKey
	body []byte
	path []string
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.path = append(f.path, r.Method+" "+r.URL.Path)
	if err := verifyJWT(f.pub, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"message":"`+err.Error()+`"}`)
		return
	}
	switch r.URL.Path {
	case "/repos/acme/widget/installation":
		io.WriteString(w, `{"id": 99}`)
	case "/app/installations/99/access_tokens":
		// An outgoing request without a body has a nil Body.
		if r.Body != nil {
			f.body, _ = io.ReadAll(r.Body)
		}
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"token": "ghs_minted"}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message":"Not Found"}`)
	}
}

func verifyJWT(pub *rsa.PublicKey, jwt string) error {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return io.ErrUnexpectedEOF
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		return err
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return err
	}
	var c struct {
		Iss string `json:"iss"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return err
	}
	now := time.Now().Unix()
	// GitHub rejects an expiry more than ten minutes ahead.
	if c.Iss != "42" || c.Iat > now || c.Exp <= now || c.Exp-c.Iat > 600 {
		return io.ErrUnexpectedEOF
	}
	return nil
}

type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	t.h.ServeHTTP(w, r)
	return w.Result(), nil
}

func fakeServer(t *testing.T) (*fakeGitHub, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeGitHub{pub: &key.PublicKey}
	// A handler in place of a listener, because a sandbox may forbid binding
	// a port.
	old := client
	client = &http.Client{Transport: handlerTransport{f}}
	t.Cleanup(func() { client = old })
	// GitHub hands out the PKCS#1 form.
	return f, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

func TestMintFindsTheInstallationAndNarrowsTheToken(t *testing.T) {
	f, key := fakeServer(t)
	tok, err := Mint(context.Background(), MintOptions{AppID: "42", Repo: "acme/widget", Repositories: []string{"widget"}, Key: key})
	if err != nil {
		t.Fatal(err)
	}
	if tok != "ghs_minted" {
		t.Fatalf("got %q", tok)
	}
	want := []string{"GET /repos/acme/widget/installation", "POST /app/installations/99/access_tokens"}
	if !slices.Equal(f.path, want) {
		t.Errorf("called %v", f.path)
	}
	if string(f.body) != `{"repositories":["widget"]}` {
		t.Errorf("sent %s", f.body)
	}
}

func TestMintReportsWhatFailed(t *testing.T) {
	_, key := fakeServer(t)
	_, err := Mint(context.Background(), MintOptions{AppID: "42", InstallationID: "5", Key: key})
	if err == nil || !strings.Contains(err.Error(), "installation 5") || !strings.Contains(err.Error(), "Not Found") {
		t.Fatalf("got %v", err)
	}
}

// The handler reads the key that nono passes down, and prints the user:token
// pair for the git route.
func TestCredentialPrintsTheToken(t *testing.T) {
	_, key := fakeServer(t)
	t.Setenv(appKeyVar, string(key))
	for _, git := range []bool{false, true} {
		args := []string{"--app-id", "42", "--installation-id", "99"}
		want := "ghs_minted"
		if git {
			args = append(args, "--git")
			want = "x-access-token:ghs_minted"
		}
		var out bytes.Buffer
		if err := credential(context.Background(), args, &out); err != nil {
			t.Fatal(err)
		}
		if out.String() != want {
			t.Errorf("got %q, want %q", out.String(), want)
		}
	}
}

func TestCredentialNeedsTheKey(t *testing.T) {
	t.Setenv(appKeyVar, "")
	err := credential(context.Background(), []string{"--app-id", "42", "--installation-id", "1"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), appKeyVar) {
		t.Fatalf("got %v", err)
	}
}
