//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/yakbd/yakbd-cli/internal/wincheck"
)

// The Windows side of the sign-in test: persistent registration, the session's layouts, ctfmon.exe, and a window
// that receives real input (SendInput) with the layout active. API names and constants as the Windows API
// documentation gives them; InstallLayoutOrTip and its flags from the reference spec (installation §4.3).

var (
	pGetKeyboardLayoutList  = user32.NewProc("GetKeyboardLayoutList")
	pActivateKeyboardLayout = user32.NewProc("ActivateKeyboardLayout")
	pRegisterClassExW       = user32.NewProc("RegisterClassExW")
	pCreateWindowExW        = user32.NewProc("CreateWindowExW")
	pDefWindowProcW         = user32.NewProc("DefWindowProcW")
	pShowWindow             = user32.NewProc("ShowWindow")
	pSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	pGetForegroundWindow    = user32.NewProc("GetForegroundWindow")
	pBringWindowToTop       = user32.NewProc("BringWindowToTop")
	pSetFocus               = user32.NewProc("SetFocus")
	pSendInput              = user32.NewProc("SendInput")
	pPeekMessageW           = user32.NewProc("PeekMessageW")
	pTranslateMessage       = user32.NewProc("TranslateMessage")
	pDispatchMessageW       = user32.NewProc("DispatchMessageW")
	pMsgWaitForMultiple     = user32.NewProc("MsgWaitForMultipleObjects")
	pGetKeyState            = user32.NewProc("GetKeyState")
	pGetModuleHandleW       = kernel32.NewProc("GetModuleHandleW")
)

const (
	ilotUninstall   = 0x01
	wmChar          = 0x0102
	wsVisibleWindow = 0x10CF0000 // WS_OVERLAPPEDWINDOW | WS_VISIBLE
	swShow          = 5
	inputKeyboard   = 1
	keyeventfKeyUp  = 0x0002
	pmRemove        = 0x0001
	qsAllInput      = 0x04FF
	waitTimeout     = 0x102
	vkLShift        = 0xA0
	vkMenu          = 0x12
	vkCapital       = 0x14
	idle            = 300 * time.Millisecond // no new message for this long: the keys are processed
)

func (s *winSystem) wow64Dir() (string, error) {
	buf := make([]uint16, 300)
	n, _, err := pGetSystemWow64Dir.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return "", fmt.Errorf("GetSystemWow64DirectoryW: %v", err)
	}
	return syscall.UTF16ToString(buf[:n]), nil
}

// installLayoutOrTip is input.dll's function, loaded by full path from the system directory.
func (s *winSystem) installLayoutOrTip() *syscall.LazyProc {
	return syscall.NewLazyDLL(filepath.Join(s.sysDir, "input.dll")).NewProc("InstallLayoutOrTip")
}

func lotString(s wincheck.Slot) string { return "0409:" + strings.ToUpper(s.KLID) }

func (s *winSystem) InstallPersistent(sl wincheck.Slot, sys32, wow64 []byte, text string) error {
	wd, err := s.wow64Dir()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.sysDir, sl.File), sys32, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(wd, sl.File), wow64, 0o644); err != nil {
		return err
	}
	if err := s.register(sl, text); err != nil {
		return err
	}
	return nil
}

// Enable adds the KLIDs to the signed-in user's input list in ONE InstallLayoutOrTip call, the entries joined by
// ";" (installation §4.3).
func (s *winSystem) Enable(slots []wincheck.Slot) error {
	p := s.installLayoutOrTip()
	if err := p.Find(); err != nil {
		return fmt.Errorf("input.dll: %v", err)
	}
	var parts []string
	for _, sl := range slots {
		parts = append(parts, lotString(sl))
	}
	list := strings.Join(parts, ";")
	str, _ := syscall.UTF16PtrFromString(list)
	if r, _, err := p.Call(uintptr(unsafe.Pointer(str)), 0); r == 0 {
		return fmt.Errorf("InstallLayoutOrTip(%s): %v", list, err)
	}
	return nil
}

func (s *winSystem) RemovePersistent(sl wincheck.Slot) error {
	var errs []string
	if pInstallLayoutOrTip := s.installLayoutOrTip(); pInstallLayoutOrTip.Find() == nil {
		str, _ := syscall.UTF16PtrFromString(lotString(sl))
		if r, _, err := pInstallLayoutOrTip.Call(uintptr(unsafe.Pointer(str)), ilotUninstall); r == 0 {
			errs = append(errs, fmt.Sprintf("InstallLayoutOrTip uninstall: %v", err))
		}
	}
	if err := s.Uninstall(sl); err != nil {
		errs = append(errs, err.Error())
	}
	if wd, err := s.wow64Dir(); err == nil {
		p := filepath.Join(wd, sl.File)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			pMoveFileExW.Call(uintptr(unsafe.Pointer(utf16p(p))), 0, movefileDelayUntilBoot)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func (s *winSystem) SessionLayouts() []uintptr {
	n, _, _ := pGetKeyboardLayoutList.Call(0, 0)
	if n == 0 {
		return nil
	}
	list := make([]uintptr, n)
	n, _, _ = pGetKeyboardLayoutList.Call(n, uintptr(unsafe.Pointer(&list[0])))
	return list[:n]
}

func (s *winSystem) Ctfmon() (bool, string) {
	var mine uint32
	pid, _, _ := pGetCurrentProcessId.Call()
	pProcessIdToSessionId.Call(pid, uintptr(unsafe.Pointer(&mine)))
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false, "CreateToolhelp32Snapshot: " + err.Error()
	}
	defer syscall.CloseHandle(snap)
	var pe syscall.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	var others []string
	for err = syscall.Process32First(snap, &pe); err == nil; err = syscall.Process32Next(snap, &pe) {
		if !strings.EqualFold(syscall.UTF16ToString(pe.ExeFile[:]), "ctfmon.exe") {
			continue
		}
		var sess uint32
		pProcessIdToSessionId.Call(uintptr(pe.ProcessID), uintptr(unsafe.Pointer(&sess)))
		if sess == mine {
			return true, fmt.Sprintf("pid %d, session %d", pe.ProcessID, sess)
		}
		others = append(others, fmt.Sprintf("pid %d in session %d", pe.ProcessID, sess))
	}
	if len(others) > 0 {
		return false, fmt.Sprintf("this session is %d; only %s", mine, strings.Join(others, ", "))
	}
	return false, fmt.Sprintf("session %d; no ctfmon.exe at all", mine)
}

// The window that receives the real input. One per process; WM_CHAR units are collected in received.
var (
	window   uintptr
	received []uint16
)

func wndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	if msg == wmChar {
		received = append(received, uint16(wparam))
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

func makeWindow() error {
	if window != 0 {
		return nil
	}
	inst, _, _ := pGetModuleHandleW.Call(0)
	class := utf16p("yakbdWincheckInput")
	wc := wndClassEx{wndProc: syscall.NewCallback(wndProc), instance: inst, className: class, background: 6}
	wc.size = uint32(unsafe.Sizeof(wc))
	if r, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("RegisterClassExW: %v", err)
	}
	h, _, err := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(utf16p("yakbd-wincheck input test"))),
		wsVisibleWindow, 100, 100, 480, 160, 0, 0, inst, 0)
	if h == 0 {
		return fmt.Errorf("CreateWindowExW: %v", err)
	}
	window = h
	pShowWindow.Call(h, swShow)
	pump(idle)
	return nil
}

// foreground brings the window to the foreground; a press of Alt first lets this process do so.
func foreground() error {
	for try := 0; try < 5; try++ {
		if fg, _, _ := pGetForegroundWindow.Call(); fg == window {
			pSetFocus.Call(window)
			return nil
		}
		sendKeys([]key{{vkMenu, false}, {vkMenu, true}}, 0)
		pSetForegroundWindow.Call(window)
		pBringWindowToTop.Call(window)
		pump(idle)
	}
	return fmt.Errorf("the test window does not get the foreground")
}

type key struct {
	vk byte
	up bool
}

// sendKeys sends key events through SendInput. INPUT is 40 bytes on 64-bit Windows and 28 on 32-bit, with the
// KEYBDINPUT at offset 8 or 4 (type DWORD, then the union aligned to a pointer).
func sendKeys(keys []key, hkl uintptr) error {
	size, ki := uintptr(28), uintptr(4)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		size, ki = 40, 8
	}
	buf := make([]byte, int(size)*len(keys))
	for i, k := range keys {
		b := buf[uintptr(i)*size:]
		*(*uint32)(unsafe.Pointer(&b[0])) = inputKeyboard
		sc, _, _ := pMapVirtualKeyExW.Call(uintptr(k.vk), mapvkVKToVSC, hkl)
		*(*uint16)(unsafe.Pointer(&b[ki])) = uint16(k.vk)
		*(*uint16)(unsafe.Pointer(&b[ki+2])) = uint16(sc)
		if k.up {
			*(*uint32)(unsafe.Pointer(&b[ki+4])) = keyeventfKeyUp
		}
	}
	n, _, err := pSendInput.Call(uintptr(len(keys)), uintptr(unsafe.Pointer(&buf[0])), size)
	if int(n) != len(keys) {
		return fmt.Errorf("SendInput sent %d of %d events: %v", n, len(keys), err)
	}
	return nil
}

// pump dispatches messages until none arrives for d (at most 10 s).
func pump(d time.Duration) {
	var msg [64]byte
	end := time.Now().Add(10 * time.Second)
	for time.Now().Before(end) {
		for {
			r, _, _ := pPeekMessageW.Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0, pmRemove)
			if r == 0 {
				break
			}
			pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg[0])))
			pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg[0])))
		}
		if r, _, _ := pMsgWaitForMultiple.Call(0, 0, 0, uintptr(d.Milliseconds()), qsAllInput); r == waitTimeout {
			return
		}
	}
}

type windowTyper struct{ hkl uintptr }

func (s *winSystem) Typer(hkl uintptr) (wincheck.Typer, error) {
	if err := makeWindow(); err != nil {
		return nil, err
	}
	if err := foreground(); err != nil {
		return nil, err
	}
	if r, _, err := pActivateKeyboardLayout.Call(hkl, 0); r == 0 {
		return nil, fmt.Errorf("ActivateKeyboardLayout: %v", err)
	}
	if st, _, _ := pGetKeyState.Call(vkCapital); st&1 != 0 { // the expectations assume Caps Lock off
		sendKeys([]key{{vkCapital, false}, {vkCapital, true}}, hkl)
	}
	pump(idle)
	return windowTyper{hkl}, nil
}

func (t windowTyper) Type(seq []wincheck.Press) ([]uint16, error) {
	if err := foreground(); err != nil {
		return nil, err
	}
	pump(idle)
	received = nil
	for _, p := range seq {
		var ks []key
		for _, m := range p.Mods {
			if m == 0x10 {
				ks = append(ks, key{vkLShift, false})
			}
		}
		ks = append(ks, key{p.VK, false}, key{p.VK, true})
		for _, m := range p.Mods {
			if m == 0x10 {
				ks = append(ks, key{vkLShift, true})
			}
		}
		if err := sendKeys(ks, t.hkl); err != nil {
			return nil, err
		}
		pump(idle)
	}
	return append([]uint16(nil), received...), nil
}

var (
	pRegOpenKeyExW  = advapi32.NewProc("RegOpenKeyExW")
	pRegEnumValueW  = advapi32.NewProc("RegEnumValueW")
	pRegGetValueW   = advapi32.NewProc("RegGetValueW")
	hkeyCurrentUser = uintptr(0x80000001)
)

const (
	keyRead  = 0x20019
	rrfRtAny = 0x0000FFFF
)

// regValues returns the string values of an HKCU key: name -> data.
func regValues(path string) map[string]string {
	out := map[string]string{}
	var key uintptr
	if r, _, _ := pRegOpenKeyExW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(utf16p(path))), 0, keyRead, uintptr(unsafe.Pointer(&key))); r != 0 {
		return out
	}
	defer pRegCloseKey.Call(key)
	for i := 0; i < 1000; i++ {
		name := make([]uint16, 256)
		data := make([]uint16, 256)
		nl, dl := uint32(len(name)), uint32(2*len(data))
		var typ uint32
		r, _, _ := pRegEnumValueW.Call(key, uintptr(i), uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&nl)), 0,
			uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&dl)))
		if r != 0 {
			break
		}
		if typ == regSZ {
			out[syscall.UTF16ToString(name[:nl])] = syscall.UTF16ToString(data[:dl/2])
		}
	}
	return out
}

func (s *winSystem) UserList(klid string) (profile, substitute bool, preload string) {
	r, _, _ := pRegGetValueW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(utf16p(`Control Panel\International\User Profile\en-US`))),
		uintptr(unsafe.Pointer(utf16p("0409:"+strings.ToUpper(klid)))), rrfRtAny, 0, 0, 0)
	profile = r == 0
	names := []string{klid}
	for n, d := range regValues(`Keyboard Layout\Substitutes`) {
		if strings.EqualFold(d, klid) {
			substitute = true
			names = append(names, n)
		}
	}
	for n, d := range regValues(`Keyboard Layout\Preload`) {
		for _, x := range names {
			if strings.EqualFold(d, x) {
				preload = n
			}
		}
	}
	return profile, substitute, preload
}
