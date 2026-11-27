//go:build !windows

// Command instcheck is the Windows test of yakbd's installers; see main_windows.go.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "instcheck runs on Windows only (inside a disposable test VM)")
	os.Exit(2)
}
