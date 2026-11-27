// Package shape checks that a layout file has the Machine and the shape of the build it is carried as
// (kbl targets/windows-install.md §3; inst §10 step 1, §2.4): x86 014C with 4-byte pointers, x64 8664, wow64 014C
// with the WOW64 build's 8-byte slots, arm64x AA64 with ARM64X metadata in the shape yakbd ships (`arm64x-x64`,
// docs/arm64x.md §5). It reads the file with the tool's own readers (target/windows, pe), so a file passes only
// when the tool would read it back as a layout of that build. `yakbd installer` checks every file before it packs
// it, and the installer program checks the files of the machine before it changes anything.
//
// The installer program (installer/stub) imports this package, and with it target/windows, pe and loss: a change
// in any of them changes the program, so `go run ./tools/buildstub` must run after it (TestEmbeddedStubIsFresh).
package shape

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/yakbd/yakbd-cli/installer/payload"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/pe"
	"github.com/yakbd/yakbd-cli/target/windows"
)

// archOf is the architecture the reader must find for each build.
var archOf = map[string]windows.Arch{
	payload.BuildX86:    windows.X86,
	payload.BuildX64:    windows.AMD64,
	payload.BuildWOW64:  windows.WOW64,
	payload.BuildARM64X: windows.ARM64X,
}

// what describes a build for the messages.
var what = map[string]string{
	payload.BuildX86:    "an x86 layout file (Machine 014C, 4-byte pointers)",
	payload.BuildX64:    "an x64 layout file (Machine 8664)",
	payload.BuildWOW64:  "a WOW64 layout file (Machine 014C, 8-byte pointer slots)",
	payload.BuildARM64X: "an ARM64X layout file (Machine AA64 with an x64 view)",
}

// Check returns nil when b is a layout file of the build's Machine and shape, and otherwise an error that says
// what the file is instead.
func Check(build string, b []byte) error {
	want, ok := archOf[build]
	if !ok {
		return fmt.Errorf("unknown build %q", build)
	}
	f, err := pe.Parse(b)
	if err != nil {
		return fmt.Errorf("the %s file is not %s: %v", build, what[build], err)
	}
	if m := uint16(f.Machine); m != payload.MachineOf[build] {
		return fmt.Errorf("the %s file is not %s: its Machine is %04X", build, what[build], m)
	}
	d, arch, err := read(b)
	if err != nil {
		return fmt.Errorf("the %s file is not %s: %v", build, what[build], err)
	}
	if arch != want {
		return fmt.Errorf("the %s file is not %s: it has the shape of a file for %s", build, what[build], arch)
	}
	if build == payload.BuildARM64X {
		if err := x64View(f, d); err != nil {
			return fmt.Errorf("the %s file is not %s: %v", build, what[build], err)
		}
	}
	return nil
}

// x64View checks the x64 view of an ARM64X file against the shipped shape `arm64x-x64` (docs/arm64x.md §3, §5):
// the ARM64X entries give Machine 8664 and a load configuration of its own, and the view's export is a plain x64
// function in an x64 range of the code map that returns the same tables as the native view.
func x64View(f *pe.File, native *windows.DLL) error {
	lc, err := f.LoadConfig()
	if err != nil || lc == nil || !lc.HasDynamicRelocs {
		return fmt.Errorf("no ARM64X entries (%v)", err)
	}
	vb, err := f.ARM64XView()
	if err != nil {
		return fmt.Errorf("its x64 view cannot be made: %v", err)
	}
	v, err := pe.Parse(vb)
	if err != nil {
		return fmt.Errorf("its x64 view: %v", err)
	}
	if v.Machine != pe.MachineAMD64 {
		return fmt.Errorf("its x64 view has Machine %04X", uint16(v.Machine))
	}
	vlc, err := v.LoadConfig()
	if err != nil || vlc == nil || vlc.RVA == lc.RVA {
		return fmt.Errorf("its x64 view has no load configuration of its own")
	}
	c, err := v.CHPEMetadata()
	if err != nil || c == nil {
		return fmt.Errorf("its x64 view has no CHPE metadata (%v)", err)
	}
	_, exps, err := v.Exports()
	if err != nil || len(exps) == 0 {
		return fmt.Errorf("its x64 view has no exports (%v)", err)
	}
	for _, e := range exps {
		if e.Ordinal == 1 && c.RangeAt(e.RVA) != pe.RangeAMD64 {
			return fmt.Errorf("the x64 view's descriptor is not x64 code")
		}
	}
	vd, arch, err := read(vb)
	if err != nil {
		return fmt.Errorf("its x64 view: %v", err)
	}
	if arch != windows.AMD64 {
		return fmt.Errorf("its x64 view has the shape of a file for %s", arch)
	}
	if !reflect.DeepEqual(vd.Tables, native.Tables) {
		return fmt.Errorf("its two views have different tables")
	}
	return nil
}

// read reads a layout file with the tool's reader and returns its first error as one line.
func read(b []byte) (*windows.DLL, windows.Arch, error) {
	im, r := windows.ReadImage(b)
	for _, e := range r.Entries {
		if e.Severity == loss.Error {
			return nil, 0, errors.New(e.Message)
		}
	}
	if im == nil {
		return nil, 0, errors.New("not a layout file")
	}
	return im.DLL, im.Arch, nil
}
