// Command nn runs a command under the nono sandbox, configured by
// .nono/nn.yml instead of a long command line.
package main

import (
	"os"

	"github.com/jderuiter/nn/internal/cli"
)

// Injected at build time via -ldflags; see mise.toml.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	cli.Version, cli.Commit, cli.Date = version, commit, date
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}
