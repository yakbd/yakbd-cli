//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "this program is a YAKBD installer (yakbd-cli-setup.exe or yakbd-gui-setup.exe); it runs on Windows only")
	os.Exit(2)
}
