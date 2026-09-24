// Command nn runs a command in a nono sandbox built from declared tools.
package main

import (
	"os"

	"github.com/jrderuiter/nn/internal/cli"
)

func main() { os.Exit(cli.Execute()) }
