package installer

import (
	"bytes"
	"debug/pe"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/convert/klckbl"
	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/installer/payload"
	"github.com/yakbd/yakbd-cli/installer/stubbuild"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/build"
)

// TestEmbeddedStubIsFresh builds the installer program as tools/buildstub does and compares it with the embedded
// file (docs/installer.md §6). CI pins the Go version the file records, so there the test always runs.
func TestEmbeddedStubIsFresh(t *testing.T) {
	if runtime.Version() != StubGoVersion() {
		if os.Getenv("CI") != "" {
			t.Fatalf("Go %s, but installer/stub-386.exe was built with %s: pin that version in CI or rebuild with go run ./tools/buildstub", runtime.Version(), StubGoVersion())
		}
		t.Skipf("Go %s; the embedded program was built with %s", runtime.Version(), StubGoVersion())
	}
	b, ver, err := stubbuild.Build("..")
	if err != nil {
		t.Fatal(err)
	}
	if ver != StubGoVersion() {
		t.Fatalf("built with %s, recorded %s", ver, StubGoVersion())
	}
	if !bytes.Equal(b, Stub()) {
		t.Fatal("installer/stub-386.exe is not a fresh build of installer/stub: run go run ./tools/buildstub and commit the result")
	}
}

func TestStubShape(t *testing.T) {
	f, err := pe.NewFile(bytes.NewReader(Stub()))
	if err != nil {
		t.Fatal(err)
	}
	oh, ok := f.OptionalHeader.(*pe.OptionalHeader32)
	if f.Machine != pe.IMAGE_FILE_MACHINE_I386 || !ok || oh.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
		t.Fatalf("machine %#x, subsystem %v", f.Machine, oh)
	}
	if end, err := payload.ImageEnd(Stub()); err != nil || end != int64(len(Stub())) {
		t.Fatalf("image end %d of %d: %v", end, len(Stub()), err)
	}
}

func kbdtst(t *testing.T) (*model.Layout, *build.Result, *windows.BuildSet) {
	t.Helper()
	b, err := os.ReadFile("../testdata/reference-spec/vm/kbdtst.klc")
	if err != nil {
		t.Fatal(err)
	}
	f, r := klc.Parse(b, "kbdtst.klc")
	if r.HasErrors() {
		t.Fatal(r.Summary())
	}
	l, _ := klckbl.FromKLC(f, "kbdtst.klc")
	res, r := build.Lower(l, build.Options{ToolVersion: [4]uint16{0, 2, 0, 0}})
	if res == nil {
		t.Fatal(r.Summary())
	}
	// A fixed set without ARM64X, whatever the default set is.
	return l, res, windows.CompileAll(res.DLL, []windows.Target{windows.TargetAMD64, windows.TargetWOW64, windows.TargetX86})
}

func TestARM64Package(t *testing.T) {
	l, res, _ := kbdtst(t)
	set := windows.CompileSet(res.DLL, []windows.Target{windows.TargetAMD64, windows.TargetWOW64, windows.TargetX86, windows.TargetARM64X}, true)
	p, r := NewPackage(l, res, set, Options{})
	if r.HasErrors() || strings.Join(p.Machines, ",") != "x64,arm64,x86" || strings.Join(p.Builds, ",") != "x64,arm64x,wow64,x86" {
		t.Fatalf("%v %v %s", p.Machines, p.Builds, r.Summary())
	}
	for _, e := range r.Entries {
		if e.Code == "windows.installerMachinesMissing" {
			t.Errorf("%s", e.Message)
		}
	}
	exe, err := ExeContainer{}.Write(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := payload.FromExe(exe)
	if err != nil || got.Data[payload.BuildARM64X] == nil {
		t.Fatalf("arm64x missing: %v", err)
	}
	one, r := NewPackage(l, res, set, Options{Machines: []string{MachineARM64}})
	if r.HasErrors() || strings.Join(one.Builds, ",") != "arm64x,wow64" {
		t.Errorf("arm64 only: %v %s", one.Builds, r.Summary())
	}
}

func TestPackageAndExe(t *testing.T) {
	l, res, set := kbdtst(t)
	p, r := NewPackage(l, res, set, Options{Tool: "yakbd test"})
	if r.HasErrors() {
		t.Fatal(r.Summary())
	}
	if strings.Join(p.Machines, ",") != "x64,x86" || strings.Join(p.Builds, ",") != "x64,wow64,x86" {
		t.Errorf("machines %v builds %v", p.Machines, p.Builds)
	}
	skipped, defaulted := false, false
	for _, e := range r.Entries {
		skipped = skipped || e.Code == "windows.installerMachinesMissing" && e.Severity == loss.Warning && strings.Contains(e.Message, "arm64")
		defaulted = defaulted || e.Code == "windowsDefault.productCode" && e.Class == loss.Defaulted
	}
	if !skipped || !defaulted {
		t.Errorf("report: %s", r.Summary())
	}
	m := p.Manifest
	if m.Layout.File != res.DLL.FileName || m.Layout.LangID != "0409" || m.Product.Code != DefaultProductCode(l.Metadata.ID, res.DLL.FileName) || !m.Enable || m.Product.Name != l.Metadata.Name {
		t.Errorf("manifest %+v", m)
	}
	exe, err := ExeContainer{}.Write(p)
	if err != nil {
		t.Fatal(err)
	}
	exe2, _ := ExeContainer{}.Write(p)
	if !bytes.Equal(exe, exe2) {
		t.Error("installer not deterministic")
	}
	got, err := payload.FromExe(append(exe, "a signature would go here"...))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range set.Builds {
		if !bytes.Equal(got.Data[buildOf[b.Target]], b.Bytes) {
			t.Errorf("%s differs", b.Target)
		}
	}
	if _, err := pe.NewFile(bytes.NewReader(exe)); err != nil {
		t.Errorf("the installer is no longer a PE file: %v", err)
	}

	no := false
	one, r := NewPackage(l, res, set, Options{Machines: []string{MachineX86}, Enable: &no})
	if r.HasErrors() || strings.Join(one.Builds, ",") != "x86" || one.Manifest.Enable {
		t.Errorf("x86 only: %v %s", one.Builds, r.Summary())
	}
	if _, r := NewPackage(l, res, set, Options{Machines: []string{MachineARM64}}); !r.HasErrors() {
		t.Error("arm64 without an arm64x build must be an error")
	}
	noWOW := &windows.BuildSet{}
	for _, b := range set.Builds {
		if b.Target != windows.TargetWOW64 {
			noWOW.Builds = append(noWOW.Builds, b)
		}
	}
	if p, _ := NewPackage(l, res, noWOW, Options{}); strings.Join(p.Machines, ",") != "x86" {
		t.Errorf("x64 must need the WOW64 file: %v", p.Machines)
	}
}

func TestDefaultProductCode(t *testing.T) {
	// RFC 9562 appendix A.4: namespace DNS, name www.example.com.
	dns := [16]byte{0x6b, 0xa7, 0xb8, 0x10, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}
	if got := uuid5(dns, "www.example.com"); got != "2ed6657d-e927-568b-95e1-2665a8aea6a2" {
		t.Errorf("uuid5: %s", got)
	}
	// kbl targets/windows-install.md section 2 (D61).
	for in, want := range map[[2]string]string{
		{"pt-t-k0-abnt2", "pttk0abn"}: "{706CBB9E-9327-5859-A4F1-8241F5A4002A}",
		{"fo-t-k0-kblwin", "kbltour"}: "{2BC7A0C6-F16B-5CCE-9C63-4360BD88AF79}",
	} {
		if got := DefaultProductCode(in[0], in[1]); got != want {
			t.Errorf("%v: %s, want %s", in, got, want)
		}
	}
	a := DefaultProductCode("fr-t-k0-azerty", "frtk0aze")
	if !payload.IsGUID(a) || a != DefaultProductCode("FR-t-k0-azerty", "FRTK0AZE.DLL") || a == DefaultProductCode("fr-t-k0-azertyx", "frtk0aze") {
		t.Errorf("%s", a)
	}
}

func TestDecideAndPin(t *testing.T) {
	l, res, _ := kbdtst(t)
	yes := false
	l.Targets.Windows.Install = &model.Install{EnableForUser: &yes, EnableForSignInAndNewUsers: &yes, ProductName: "N", ProductVersion: "v", Publisher: "P"}
	d, r := Decide(l, res.Pins)
	if d.Enable || d.ProductName != "N" || d.ProductVersion != "v" || d.Publisher != "P" || len(r.Entries) != 1 || r.Entries[0].Code != "windowsDefault.productCode" {
		t.Errorf("%+v %s", d, r.Summary())
	}
	for _, c := range []struct{ klid, id, lang string }{{"a0000407", "", "0409"}, {"", "0000", "0409"}, {"", "1000", "0409"}, {"", "", "2400"}, {"", "", "1000"}} {
		reg := *res.Pins.Registration
		reg.KLID, reg.LayoutID, reg.LangID = c.klid, c.id, c.lang
		pins := *res.Pins
		pins.Registration = &reg
		if _, r := Decide(l, &pins); !r.HasErrors() || r.Entries[len(r.Entries)-1].Code != "windows.registrationInvalid" {
			t.Errorf("%+v accepted", c)
		}
	}
	// D87: the option is read from the file; with LANGID 0C00 the build fails.
	on := true
	l.Targets.Windows.Install = &model.Install{EnableForSignInAndNewUsers: &on}
	if d, r := Decide(l, res.Pins); !d.SignIn || r.HasErrors() {
		t.Errorf("enableForSignInAndNewUsers true: %+v %s", d, r.Summary())
	}
	reg := *res.Pins.Registration
	reg.LangID = "0C00"
	pins := *res.Pins
	pins.Registration = &reg
	if _, r := Decide(l, &pins); !r.HasErrors() || r.Entries[len(r.Entries)-1].Code != "windows.registrationInvalid" {
		t.Errorf("0C00 with the sign-in option accepted: %s", r.Summary())
	}
	l.Targets.Windows.Install = nil
	pp, pr := build.Pin(l, build.Options{ToolVersion: [4]uint16{0, 2, 0, 0}})
	if pp == nil {
		t.Fatal(pr.Summary())
	}
	pinned := *pp
	if r := Pin(&pinned); r.HasErrors() {
		t.Fatal(r.Summary())
	}
	w := pinned.Targets.Windows
	if w.Registration.ProductCode != DefaultProductCode(l.Metadata.ID, "kbdtst") || w.Install == nil || !*w.Install.EnableForUser || w.Install.EnableForSignInAndNewUsers == nil || *w.Install.EnableForSignInAndNewUsers || w.Install.ProductName != l.Metadata.Name {
		t.Errorf("pinned %+v %+v", w.Registration, w.Install)
	}
	if _, r := Decide(&pinned, w); len(r.Entries) != 0 {
		t.Errorf("a pinned file still uses rules: %s", r.Summary())
	}
}

// TestBadShapeNotPacked: a build set whose wow64 file is the x86 file (right Machine, wrong shape) gives no
// package (the check of installer/shape, which the installer program repeats on the machine).
func TestBadShapeNotPacked(t *testing.T) {
	l, res, set := kbdtst(t)
	bad := &windows.BuildSet{}
	var x86 []byte
	for _, b := range set.Builds {
		if b.Target == windows.TargetX86 {
			x86 = b.Bytes
		}
	}
	for _, b := range set.Builds {
		if b.Target == windows.TargetWOW64 {
			b.Bytes = x86
		}
		bad.Builds = append(bad.Builds, b)
	}
	_, r := NewPackage(l, res, bad, Options{})
	found := false
	for _, e := range r.Entries {
		found = found || e.Code == "installer.fileShape" && e.Severity == loss.Error && strings.Contains(e.Message, "wow64")
	}
	if !found {
		t.Errorf("not refused: %s", r.Summary())
	}
}

func TestToolProductCode(t *testing.T) {
	if got := ToolProductCode(); got != "{EBE1EAEC-C574-5473-8489-B3FF22FC6506}" {
		t.Errorf("%s (docs/installer-cli.md §12 names {EBE1EAEC-C574-5473-8489-B3FF22FC6506})", got)
	}
}
