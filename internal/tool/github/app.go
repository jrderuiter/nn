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
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// appKeyVar carries the App private key from nn to nono, and from nono to the
// capture command. nn resolves it once at launch, so a mint never calls fnox.
const appKeyVar = "NN_GITHUB_APP_KEY"

const apiURL = "https://api.github.com"

// client is a variable so a test can answer without a network.
var client = http.DefaultClient

// MintOptions says which installation token to mint.
type MintOptions struct {
	AppID string
	// InstallationID is looked up from Repo when it is empty.
	InstallationID string
	// Repo is owner/name.
	Repo string
	// Repositories narrows the token to these repository names.
	Repositories []string
	// Key is the App private key in PEM form.
	Key []byte
}

// Mint returns a new installation token for a GitHub App.
func Mint(ctx context.Context, o MintOptions) (string, error) {
	key, err := parseKey(o.Key)
	if err != nil {
		return "", err
	}
	jwt, err := appJWT(o.AppID, key, time.Now())
	if err != nil {
		return "", err
	}
	id := o.InstallationID
	if id == "" {
		if o.Repo == "" {
			return "", errors.New("an installation id or a repository is required")
		}
		var inst struct {
			ID int64 `json:"id"`
		}
		if err := call(ctx, http.MethodGet, "/repos/"+o.Repo+"/installation", jwt, nil, &inst); err != nil {
			return "", fmt.Errorf("find the installation for %s: %w", o.Repo, err)
		}
		id = fmt.Sprint(inst.ID)
	}
	var body any
	if len(o.Repositories) > 0 {
		body = map[string][]string{"repositories": o.Repositories}
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := call(ctx, http.MethodPost, "/app/installations/"+id+"/access_tokens", jwt, body, &tok); err != nil {
		return "", fmt.Errorf("create a token for installation %s: %w", id, err)
	}
	if tok.Token == "" {
		return "", fmt.Errorf("create a token for installation %s: the response holds no token", id)
	}
	return tok.Token, nil
}

// parseKey reads the PKCS#1 form that GitHub hands out, and the PKCS#8 form
// that a conversion with openssl produces.
func parseKey(b []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("the App private key is not in PEM form")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse the App private key: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the App private key is not an RSA key")
	}
	return rk, nil
}

// appJWT signs the short-lived token that authenticates as the App itself.
// The issue time is a minute in the past to allow for clock drift, and GitHub
// rejects an expiry more than ten minutes ahead.
func appJWT(appID string, key *rsa.PrivateKey, now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": appID,
	})
	if err != nil {
		return "", err
	}
	signed := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign the App token: %w", err)
	}
	return signed + "." + enc.EncodeToString(sig), nil
}

func call(ctx context.Context, method, path, jwt string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiURL+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(b, &e)
		return fmt.Errorf("github returned %s: %s", resp.Status, e.Message)
	}
	return json.Unmarshal(b, out)
}

// credential is the handler behind `nn credential github`. nono runs it on
// the host whenever its cached token is about to expire.
func credential(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("github", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var o MintOptions
	var git bool
	fs.StringVar(&o.AppID, "app-id", "", "")
	fs.StringVar(&o.InstallationID, "installation-id", "", "")
	fs.StringVar(&o.Repo, "repo", "", "")
	fs.Func("repository", "", func(v string) error {
		o.Repositories = append(o.Repositories, v)
		return nil
	})
	fs.BoolVar(&git, "git", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if o.AppID == "" {
		return errors.New("--app-id is required")
	}
	key := os.Getenv(appKeyVar)
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("%s is not set", appKeyVar)
	}
	o.Key = []byte(key)
	token, err := Mint(ctx, o)
	if err != nil {
		return err
	}
	if git {
		// GitHub takes a token as the password for this user name.
		token = "x-access-token:" + token
	}
	_, err = fmt.Fprint(out, token)
	return err
}
