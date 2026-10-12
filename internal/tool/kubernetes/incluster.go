package kubernetes

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// defaultServiceAccountDir is where the kubelet mounts a pod's own identity.
const defaultServiceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// defaultAPIServer is the in-cluster address of the API server.
//
// It is a name rather than the KUBERNETES_SERVICE_HOST address, which is
// normally a cluster IP. nono's allowlist and its TLS interception both work on
// domains, and every API server certificate carries this name as a subject
// alternative name.
const defaultAPIServer = "https://kubernetes.default.svc"

// podIdentity is what the kubelet mounts into a pod.
type podIdentity struct {
	// Dir is the directory the three files came from.
	Dir string
	// Namespace is the namespace the pod runs in.
	Namespace string
	// ServiceAccount is the account the pod runs as, decoded from the token.
	// It is empty when the token does not say.
	ServiceAccount string
	TokenFile      string
	CAFile         string
}

// readPodIdentity reads the identity the kubelet mounted, without reading the
// token itself any further than its claims.
func readPodIdentity(dir string) (*podIdentity, error) {
	id := &podIdentity{
		Dir:       dir,
		TokenFile: filepath.Join(dir, "token"),
		CAFile:    filepath.Join(dir, "ca.crt"),
	}
	token, err := os.ReadFile(id.TokenFile)
	if err != nil {
		return nil, fmt.Errorf("read the service account token %s: %w.\n"+
			"  %s does not look like a mounted service account, so this is probably not a pod", id.TokenFile, err, dir)
	}
	if len(strings.TrimSpace(string(token))) == 0 {
		return nil, fmt.Errorf("the service account token %s is empty", id.TokenFile)
	}
	if _, err := os.Stat(id.CAFile); err != nil {
		return nil, fmt.Errorf("read the cluster certificate authority %s: %w", id.CAFile, err)
	}
	ns, err := os.ReadFile(filepath.Join(dir, "namespace"))
	if err != nil {
		return nil, fmt.Errorf("read the namespace %s: %w", filepath.Join(dir, "namespace"), err)
	}
	id.Namespace = strings.TrimSpace(string(ns))
	if id.Namespace == "" {
		return nil, fmt.Errorf("the namespace file %s is empty", filepath.Join(dir, "namespace"))
	}
	id.ServiceAccount = serviceAccountFromToken(token)
	return id, nil
}

// serviceAccountFromToken reads the account name out of a service account
// token, whose subject claim is system:serviceaccount:<namespace>:<name>.
//
// It verifies no signature, because it makes no access decision. It only
// chooses between two local code paths: use the mounted token as it is, or mint
// a token for some other account. A token it cannot read returns an empty name,
// which takes the minting path, and minting fails loudly if it was the wrong
// choice.
func serviceAccountFromToken(token []byte) string {
	parts := strings.Split(strings.TrimSpace(string(token)), ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Subject string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	const prefix = "system:serviceaccount:"
	if !strings.HasPrefix(claims.Subject, prefix) {
		return ""
	}
	_, name, found := strings.Cut(strings.TrimPrefix(claims.Subject, prefix), ":")
	if !found {
		return ""
	}
	return name
}

// looksLikePod reports whether this process is running in a pod.
//
// Nothing branches on it. nn never guesses the mode, because a guess would make
// the same command mean different things in different places. It is read once,
// to turn an unreadable kubeconfig into an error that names the setting the
// user wanted.
func looksLikePod(lookup func(string) (string, bool), dir string) bool {
	if lookup == nil {
		return false
	}
	if v, ok := lookup("KUBERNETES_SERVICE_HOST"); !ok || v == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "token"))
	return err == nil
}
