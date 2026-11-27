package main

import (
	"bytes"
	"debug/pe"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestMarkerDLL reads the marker DLL back with debug/pe: a 32-bit DLL whose entry point is the start of .text and
// which imports exactly the three kernel32 functions its code calls.
func TestMarkerDLL(t *testing.T) {
	b, err := markerDLL()
	if err != nil {
		t.Fatal(err)
	}
	f, err := pe.NewFile(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if f.Machine != pe.IMAGE_FILE_MACHINE_I386 || f.Characteristics&pe.IMAGE_FILE_DLL == 0 {
		t.Errorf("machine %#x characteristics %#x", f.Machine, f.Characteristics)
	}
	oh := f.OptionalHeader.(*pe.OptionalHeader32)
	text := f.Section(".text")
	if text == nil || oh.AddressOfEntryPoint != text.VirtualAddress {
		t.Errorf("entry point %#x", oh.AddressOfEntryPoint)
	}
	syms, err := f.ImportedSymbols()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(syms)
	if got := strings.Join(syms, " "); got != "CloseHandle:KERNEL32.dll CreateFileW:KERNEL32.dll GetModuleFileNameW:KERNEL32.dll" {
		t.Errorf("imports %s", got)
	}
	code, _ := text.Data()
	// Each call [IAT slot] holds the virtual address of its slot: GetModuleFileNameW, CreateFileW, CloseHandle.
	idata := f.Section(".idata")
	for i, at := range []int{31, 86, 98} {
		va := uint32(code[at]) | uint32(code[at+1])<<8 | uint32(code[at+2])<<16 | uint32(code[at+3])<<24
		if want := oh.ImageBase + idata.VirtualAddress + 56 + uint32(4*i); va != want {
			t.Errorf("call %d: %#x, want %#x", i, va, want)
		}
	}
}

// TestPlantDryRun checks the planting bundle's phases as far as Linux can: every phase the driver runs is a plant
// phase of tools/instcheck, dispatched before the other phase groups (which open other bundles' files); every
// bundle file the checker's plant code names is one the bundle writes (plantFiles).
func TestPlantDryRun(t *testing.T) {
	src, err := os.ReadFile("../instcheck/plant_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`var plantPhases = \[\]string\{([^}]*)\}`).FindSubmatch(src)
	if m == nil {
		t.Fatal("plantPhases not found")
	}
	phases := map[string]bool{}
	for _, q := range regexp.MustCompile(`"([^"]+)"`).FindAllSubmatch(m[1], -1) {
		phases[string(q[1])] = true
	}
	ran := map[string]bool{}
	for _, q := range regexp.MustCompile(`(?m)^\s*(?:Task|Phase) \$s (\S+)`).FindAllStringSubmatch(drivePlantPS1, -1) {
		ran[q[1]] = true
		if !phases[q[1]] {
			t.Errorf("driver runs %s, not a plant phase", q[1])
		}
	}
	for p := range phases {
		if !ran[p] {
			t.Errorf("phase %s never run by the driver", p)
		}
	}
	if !strings.Contains(drivePlantPS1, "Collect $s legP w11plant") {
		t.Error("driver does not collect the w11plant summary")
	}
	files := map[string]bool{}
	for _, f := range plantFiles {
		files[f] = true
	}
	for _, q := range regexp.MustCompile(`"([A-Za-z0-9_.-]+\.(?:exe|dll))"`).FindAllSubmatch(src, -1) {
		n := string(q[1])
		if n == probeLikeName || strings.HasPrefix(n, "wtsapi32") {
			continue // names loaded or planted, not bundle files
		}
		if !files[n] {
			t.Errorf("checker names %s, which the bundle does not write", n)
		}
	}
	// The control must prove the marker DLL runs without depending on a program started inside a planted folder
	// (run 1 of the bundle: such a copy reached no load): the bundle's own helper loads the probe by full path.
	if !strings.Contains(string(src), `helper := filepath.Join(c.bundle, "instcheck-32.exe")`) ||
		!strings.Contains(string(src), `exec.Command(helper, "-loadbare", filepath.Join(dir, probeName))`) {
		t.Error("plant-control does not load the probe by full path from the bundle's helper")
	}
	main, err := os.ReadFile("../instcheck/main_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`else if !c\.plant\(\*phase\) &&`).Match(main) {
		t.Error("instcheck does not dispatch plant phases first (the other groups open demo-setup.exe)")
	}
}

const probeLikeName = "yakbdplantprobe.dll"
