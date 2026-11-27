//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "this program is the Windows part of a yakbd installer; it runs on Windows only")
	os.Exit(2)
}
