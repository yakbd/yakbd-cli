//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "this program is yakbd-setup.exe, the Windows installer of yakbd; it runs on Windows only")
	os.Exit(2)
}
