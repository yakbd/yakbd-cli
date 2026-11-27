//go:build windows

package main

import (
	"os"

	"github.com/yakbd/yakbd-cli/installer/winhost"
)

func main() {
	os.Exit(run(os.Args[1:], &win{winhost.New()}))
}
