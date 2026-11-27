//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"github.com/yakbd/yakbd-cli/internal/wincheck"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	pLoadKeyboardLayoutW  = user32.NewProc("LoadKeyboardLayoutW")
	pUnloadKeyboardLayout = user32.NewProc("UnloadKeyboardLayout")
	pToUnicodeEx          = user32.NewProc("ToUnicodeEx")
	pMapVirtualKeyExW     = user32.NewProc("MapVirtualKeyExW")
	pRegCreateKeyExW      = advapi32.NewProc("RegCreateKeyExW")
	pRegSetValueExW       = advapi32.NewProc("RegSetValueExW")
	pRegCloseKey          = advapi32.NewProc("RegCloseKey")
	pRegDeleteKeyW        = advapi32.NewProc("RegDeleteKeyW")
	pIsWow64Process       = kernel32.NewProc("IsWow64Process")
	pIsWow64Process2      = kernel32.NewProc("IsWow64Process2")
	pGetCurrentProcess    = kernel32.NewProc("GetCurrentProcess")
	pVirtualAlloc         = kernel32.NewProc("VirtualAlloc")
	pVirtualFree          = kernel32.NewProc("VirtualFree")
	pMoveFileExW          = kernel32.NewProc("MoveFileExW")
	pGetSystemDirectoryW  = kernel32.NewProc("GetSystemDirectoryW")
	pGetSystemWow64Dir    = kernel32.NewProc("GetSystemWow64DirectoryW")
)

// Constants of the Windows API (names as in the API documentation).
const (
	hkeyLocalMachine       = 0x80000002
	keyAllAccess           = 0xF003F
	regSZ                  = 1
	klfActivate            = 0x00000001
	mapvkVKToVSC           = 0
	memReserve             = 0x2000
	memRelease             = 0x8000
	pageNoAccess           = 0x01
	movefileDelayUntilBoot = 0x4
	imageFileMachineARM64  = 0xAA64
)

const layoutsKey = `SYSTEM\CurrentControlSet\Control\Keyboard Layouts\`

type winSystem struct {
	build  string
	sysDir string // the system directory as this process sees it (System32 or, redirected, SysWOW64)
}

func newSystem() (system, error) {
	s := &winSystem{build: "x64"}
	switch {
	case runtime.GOARCH == "arm64":
		s.build = "arm64"
	case unsafe.Sizeof(uintptr(0)) == 8 && pIsWow64Process2.Find() == nil:
		// An x64 process on ARM64 Windows: IsWow64Process2 gives process machine 0 and native machine AA64
		// (installation tests/vm/arm64 README, "Traps"). It loads the ARM64 Windows file of System32.
		proc, _, _ := pGetCurrentProcess.Call()
		var pm, nm uint16
		if r, _, _ := pIsWow64Process2.Call(proc, uintptr(unsafe.Pointer(&pm)), uintptr(unsafe.Pointer(&nm))); r != 0 && nm == imageFileMachineARM64 {
			s.build = "x64-on-arm64"
		}
	}
	if unsafe.Sizeof(uintptr(0)) == 4 {
		s.build = "x86"
		proc, _, _ := pGetCurrentProcess.Call()
		var wow int32
		if r, _, _ := pIsWow64Process.Call(proc, uintptr(unsafe.Pointer(&wow))); r != 0 && wow != 0 {
			s.build = "wow64"
		}
	}
	buf := make([]uint16, 300)
	n, _, err := pGetSystemDirectoryW.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return nil, fmt.Errorf("GetSystemDirectoryW: %v", err)
	}
	s.sysDir = syscall.UTF16ToString(buf[:n])
	return s, nil
}

func (s *winSystem) Build() string { return s.build }

func utf16p(v string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(v)
	return p
}

func (s *winSystem) setString(key uintptr, name, value string) error {
	data, _ := syscall.UTF16FromString(value)
	r, _, _ := pRegSetValueExW.Call(key, uintptr(unsafe.Pointer(utf16p(name))), 0, regSZ,
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2))
	if r != 0 {
		return fmt.Errorf("RegSetValueExW %s: %v", name, syscall.Errno(r))
	}
	return nil
}

// Install copies the DLL into the system directory (a 32-bit process on 64-bit Windows lands in SysWOW64) and
// writes the three values of installation §3.2 under a new test KLID.
func (s *winSystem) Install(sl wincheck.Slot, dll []byte, text string) error {
	if err := os.WriteFile(filepath.Join(s.sysDir, sl.File), dll, 0o644); err != nil {
		return err
	}
	return s.register(sl, text)
}

// register writes Layout File, Layout Text and Layout Id under a new test KLID.
func (s *winSystem) register(sl wincheck.Slot, text string) error {
	var key uintptr
	var disp uint32
	r, _, _ := pRegCreateKeyExW.Call(hkeyLocalMachine, uintptr(unsafe.Pointer(utf16p(layoutsKey+sl.KLID))), 0, 0, 0,
		keyAllAccess, 0, uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&disp)))
	if r != 0 {
		return fmt.Errorf("RegCreateKeyExW %s: %v (run as administrator)", sl.KLID, syscall.Errno(r))
	}
	defer pRegCloseKey.Call(key)
	if disp != 1 { // REG_CREATED_NEW_KEY
		return fmt.Errorf("KLID %s exists already; restore the virtual machine's checkpoint", sl.KLID)
	}
	for _, v := range [][2]string{{"Layout File", sl.File}, {"Layout Text", text}, {"Layout Id", fmt.Sprintf("%04x", sl.LayoutID)}} {
		if err := s.setString(key, v[0], v[1]); err != nil {
			return err
		}
	}
	return nil
}

// Uninstall removes the registry key and the file; a file that is still locked is removed at the next start.
func (s *winSystem) Uninstall(sl wincheck.Slot) error {
	var errs []string
	if r, _, _ := pRegDeleteKeyW.Call(hkeyLocalMachine, uintptr(unsafe.Pointer(utf16p(layoutsKey+sl.KLID)))); r != 0 && r != 2 {
		errs = append(errs, fmt.Sprintf("RegDeleteKeyW %s: %v", sl.KLID, syscall.Errno(r)))
	}
	p := filepath.Join(s.sysDir, sl.File)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		pMoveFileExW.Call(uintptr(unsafe.Pointer(utf16p(p))), 0, movefileDelayUntilBoot)
		errs = append(errs, sl.File+" is locked; removed at the next start")
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func (s *winSystem) Load(klid string) (uintptr, error) {
	h, _, err := pLoadKeyboardLayoutW.Call(uintptr(unsafe.Pointer(utf16p(klid))), klfActivate)
	if h == 0 {
		code := uintptr(0)
		if en, ok := err.(syscall.Errno); ok {
			code = uintptr(en)
		}
		return 0, fmt.Errorf("NULL (GetLastError %d: %v)", code, err)
	}
	return h, nil
}

func (s *winSystem) Unload(hkl uintptr) { pUnloadKeyboardLayout.Call(hkl) }

func (s *winSystem) Prober(hkl uintptr) wincheck.Prober { return &prober{hkl: hkl} }

type prober struct{ hkl uintptr }

// ToUnicodeEx with the modifier keys of the press held (generic and left keys) and nothing else. Two values are
// special (target/windows.Press): A1 is the right Shift key (generic Shift and VK_RSHIFT down), 14 is Caps Lock
// toggled on (low bit of VK_CAPITAL), not held.
func (p *prober) ToUnicode(k wincheck.Press, flags uint32) (int, []uint16) {
	var state [256]byte
	for _, m := range k.Mods {
		state[m] = 0x80
		switch m {
		case 0xA1:
			state[0x10] = 0x80
		case 0x14:
			state[0x14] = 0x01
		case 0x10:
			state[0xA0] = 0x80
		case 0x11:
			state[0xA2] = 0x80
		case 0x12:
			state[0xA4] = 0x80
		}
	}
	sc, _, _ := pMapVirtualKeyExW.Call(uintptr(k.VK), mapvkVKToVSC, p.hkl)
	var buf [64]uint16
	r, _, _ := pToUnicodeEx.Call(uintptr(k.VK), sc, uintptr(unsafe.Pointer(&state[0])),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(flags), p.hkl)
	rc := int(int32(r))
	switch {
	case rc < 0 && buf[0] == 0:
		return rc, nil // a chained step writes nothing
	case rc < 0:
		return rc, buf[:1]
	case rc > len(buf):
		rc = len(buf)
	}
	return rc, append([]uint16(nil), buf[:rc]...)
}

func (s *winSystem) Occupy(base uint64, size uint32) (func(), error) {
	a, _, err := pVirtualAlloc.Call(uintptr(base), uintptr(size), memReserve, pageNoAccess)
	if a == 0 {
		return nil, fmt.Errorf("VirtualAlloc at %#x: %v", base, err)
	}
	return func() { pVirtualFree.Call(a, 0, memRelease) }, nil
}

// SystemFile reads a file of this process's system directory (inside the guest only).
func (s *winSystem) SystemFile(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.sysDir, name))
}

func (s *winSystem) SysWOW64Layouts() (map[string][]byte, error) {
	buf := make([]uint16, 300)
	n, _, err := pGetSystemWow64Dir.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return nil, fmt.Errorf("GetSystemWow64DirectoryW: %v", err)
	}
	dir := syscall.UTF16ToString(buf[:n])
	names, _ := filepath.Glob(filepath.Join(dir, "kbd*.dll"))
	out := map[string][]byte{}
	for _, p := range names {
		if b, err := os.ReadFile(p); err == nil { // read as data, never loaded
			out[filepath.Base(p)] = b
		}
	}
	return out, nil
}

var (
	pOpenInputDesktop     = user32.NewProc("OpenInputDesktop")
	pCloseDesktop         = user32.NewProc("CloseDesktop")
	pProcessIdToSessionId = kernel32.NewProc("ProcessIdToSessionId")
	pGetCurrentProcessId  = kernel32.NewProc("GetCurrentProcessId")
)

const desktopReadObjects = 0x0001

// Interactive checks for a session other than 0 whose input desktop this process can open; without one
// LoadKeyboardLayoutW returns NULL (VM run 1: PowerShell Direct).
func (s *winSystem) Interactive() error {
	const hint = "run it at the console, or as a scheduled task with an interactive logon and highest privileges (README.txt)"
	pid, _, _ := pGetCurrentProcessId.Call()
	var session uint32
	if r, _, _ := pProcessIdToSessionId.Call(pid, uintptr(unsafe.Pointer(&session))); r != 0 && session == 0 {
		return fmt.Errorf("this process runs in session 0, which has no interactive desktop: %s", hint)
	}
	d, _, err := pOpenInputDesktop.Call(0, 0, desktopReadObjects)
	if d == 0 {
		return fmt.Errorf("no interactive desktop (OpenInputDesktop: %v): %s", err, hint)
	}
	pCloseDesktop.Call(d)
	return nil
}
