// Package stubbuild builds the installer program (installer/stub) the one way that ships: for windows/386 with
// fixed flags and environment, so that the result is reproducible (docs/installer.md §6). tools/buildstub writes
// it into installer/stub-386.exe; a test of package installer compares a fresh build with the embedded file.
package stubbuild

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Package is the import path of the installer program.
const Package = "github.com/yakbd/yakbd-cli/installer/stub"

// Flags are the go build flags of the shipped program.
var Flags = []string{"-trimpath", "-buildvcs=false", "-ldflags=-s -w -H=windowsgui -buildid="}

// Env is the environment that overrides the caller's for the build.
var Env = []string{"GOOS=windows", "GOARCH=386", "GO386=sse2", "CGO_ENABLED=0", "GOFLAGS=", "GOEXPERIMENT=", "GOTOOLCHAIN=local"}

// Build builds the program from the module at root and returns its bytes and the Go version used.
func Build(root string) ([]byte, string, error) {
	tmp, err := os.MkdirTemp("", "yakbd-stub-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, "stub.exe")
	args := append([]string{"build", "-o", out}, Flags...)
	cmd := exec.Command("go", append(args, Package)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), Env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, "", fmt.Errorf("go build: %v\n%s", err, stderr.String())
	}
	b, err := os.ReadFile(out)
	if err != nil {
		return nil, "", err
	}
	v := exec.Command("go", "env", "GOVERSION")
	v.Dir = root
	v.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	ver, err := v.Output()
	if err != nil {
		return nil, "", fmt.Errorf("go env GOVERSION: %v", err)
	}
	return b, strings.TrimSpace(string(ver)), nil
}
