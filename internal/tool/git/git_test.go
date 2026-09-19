package git

import (
	"context"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/jrderuiter/nn/internal/tool"
)

func build(t *testing.T, body string, sock string) *tool.Result {
	t.Helper()
	var cfg struct {
		Tools map[string]toml.Primitive `toml:"tools"`
	}
	md, err := toml.Decode("[tools.git]\n"+body, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(md, cfg.Tools["git"])
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Build(context.Background(), &tool.Env{
		Workdir: "/w",
		Lookup: func(k string) (string, bool) {
			if k == "SSH_AUTH_SOCK" {
				return sock, sock != ""
			}
			return "", false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func has(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// SSH_AUTH_SOCK names a socket. Passing the name without granting the socket
// only turns a clear configuration choice into a connection error.
func TestSSHAuthSockIsAllowedOnlyWithTheSocket(t *testing.T) {
	off := build(t, "", "")
	if has(off.Fragment.Environment.AllowVars, "SSH_AUTH_SOCK") {
		t.Error("with ssh off the variable must not be allowed through")
	}
	if off.Fragment.Filesystem != nil {
		t.Error("with ssh off no socket is granted")
	}

	on := build(t, "ssh = true\n", "/tmp/agent.sock")
	if !has(on.Fragment.Environment.AllowVars, "SSH_AUTH_SOCK") {
		t.Error("with ssh on the variable has to come through")
	}
	if len(on.Fragment.Filesystem.UnixSocket) != 1 {
		t.Error("with ssh on the socket has to be granted")
	}
}

func TestIdentityNeedsBothHalves(t *testing.T) {
	var cfg struct {
		Tools map[string]toml.Primitive `toml:"tools"`
	}
	md, _ := toml.Decode("[tools.git]\nname = \"Jane\"\n", &cfg)
	if _, err := New(md, cfg.Tools["git"]); err == nil {
		t.Fatal("a name without an email must be an error")
	}
}
