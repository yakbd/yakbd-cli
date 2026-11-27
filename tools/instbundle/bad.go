package main

import (
	"os"
	"path/filepath"

	"github.com/yakbd/yakbd-cli/installer"
	"github.com/yakbd/yakbd-cli/installer/payload"
)

// badInstallers writes, from demo-setup.exe, two installers that `yakbd installer` would never write (its shape
// check refuses them; this test-only path builds the payload directly): demo-badshape-setup.exe carries the x86
// file as its wow64 file (right Machine and hashes, 4-byte pointers instead of 8-byte slots), and
// demo-damaged-setup.exe a truncated x64 file (right Machine and hashes, sections beyond the file).
func badInstallers(out string) error {
	b, err := os.ReadFile(filepath.Join(out, "demo-setup.exe"))
	if err != nil {
		return err
	}
	p, err := payload.FromExe(b)
	if err != nil {
		return err
	}
	edits := map[string]func(map[string][]byte){
		"demo-badshape-setup.exe": func(d map[string][]byte) { d[payload.BuildWOW64] = d[payload.BuildX86] },
		"demo-damaged-setup.exe":  func(d map[string][]byte) { d[payload.BuildX64] = d[payload.BuildX64][:0x400] },
		// The same two for x86 Windows, which gets only the x86 file.
		"demo-badx86-setup.exe":     func(d map[string][]byte) { d[payload.BuildX86] = d[payload.BuildWOW64] },
		"demo-damagedx86-setup.exe": func(d map[string][]byte) { d[payload.BuildX86] = d[payload.BuildX86][:0x400] },
	}
	for name, edit := range edits {
		data := map[string][]byte{}
		for k, v := range p.Data {
			data[k] = v
		}
		edit(data)
		exe, err := installer.ExeContainer{}.Write(&installer.Package{Manifest: *p.Manifest, Builds: p.Manifest.Builds(), Data: data})
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(out, name), exe, 0o644); err != nil {
			return err
		}
	}
	return nil
}
