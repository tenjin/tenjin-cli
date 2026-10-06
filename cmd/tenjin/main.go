// Command tenjin is the Tenjin CLI.
package main

import (
	"os"

	"github.com/tenjin/tenjin-cli/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
