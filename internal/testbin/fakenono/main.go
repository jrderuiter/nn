// Command fakenono stands in for the nono binary in integration tests. It
// reports the argv and environment it was handed, which is the only way to
// observe what syscall.Exec actually passed on.
package main

import (
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
)

type record struct {
	// Exe is the file that actually ran. argv[0] is whatever the caller
	// passed to exec, which is not the same thing — that distinction is the
	// whole point of the PATH-resolution test.
	Exe   string            `json:"exe"`
	Argv  []string          `json:"argv"`
	Env   map[string]string `json:"env"`
	Stdin string            `json:"stdin"`
}

func main() {
	exe, _ := os.Executable()
	rec := record{Exe: exe, Argv: os.Args, Env: map[string]string{}}

	// Report only the names the test asked about, so the dump stays readable.
	want := map[string]bool{}
	for _, k := range strings.Split(os.Getenv("FAKE_ENV_REPORT"), ",") {
		if k != "" {
			want[k] = true
		}
	}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && want[k] {
			rec.Env[k] = v
		}
	}

	if os.Getenv("FAKE_READ_STDIN") != "" {
		if b, err := io.ReadAll(os.Stdin); err == nil {
			rec.Stdin = string(b)
		}
	}

	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(rec); err != nil {
		os.Exit(1)
	}
	code, _ := strconv.Atoi(os.Getenv("FAKE_EXIT"))
	os.Exit(code)
}
