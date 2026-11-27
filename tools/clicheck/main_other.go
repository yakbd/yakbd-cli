//go:build !windows

// Command clicheck runs in a Windows guest only (tools/clibundle).
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "clicheck runs on Windows only")
	os.Exit(2)
}
