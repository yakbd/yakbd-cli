package main

import (
	"os"
	"testing"

	"github.com/yakbd/yakbd-cli/installer"
	"github.com/yakbd/yakbd-cli/installer/toolsetup"
)

// TestWrite builds yakbd-setup.exe (three yakbd builds and the program; skipped with -short) and reads its payload
// back: every build, the version, the product code.
func TestWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("builds four programs")
	}
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	b, err := Write("9.8.7.6")
	if err != nil {
		t.Fatal(err)
	}
	p, err := toolsetup.FromExe(b)
	if err != nil {
		t.Fatal(err)
	}
	m := p.Manifest
	if m.Product.Version != "9.8.7.6" || m.Product.Code != installer.ToolProductCode() || len(p.Data) != 3 {
		t.Errorf("%+v, %d builds", m.Product, len(p.Data))
	}
}
