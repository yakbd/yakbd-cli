// Command yakbd converts, validates and compiles keyboard layouts. See internal/cli for the commands.
package main

import (
	"os"

	"github.com/yakbd/yakbd-cli/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
