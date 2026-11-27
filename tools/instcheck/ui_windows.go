//go:build windows

package main

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Driving the installer's own dialogs as a person would (the double-click path). The installer's dialogs
// are message boxes of its first, unelevated process (the elevated copy shows none, docs/installer.md §3), so a
// process of the same user can read them and press their buttons with window messages: WM_GETTEXT to read every
// child window's text, WM_COMMAND with the button's id (IDYES, IDNO, IDOK) to press it, the same message a click on
// the button sends. The elevation prompt is not driven here: it is on the secure desktop, which no process of the
// user can reach; the runner answers it from the host (README.txt), told by the marker file uac-waiting.txt.

var (
	shell32 = syscall.NewLazyDLL("shell32.dll")

	pShellExecuteExW           = shell32.NewProc("ShellExecuteExW")
	pEnumWindows               = user32.NewProc("EnumWindows")
	pEnumChildWindows          = user32.NewProc("EnumChildWindows")
	pGetWindowThreadProcessId  = user32.NewProc("GetWindowThreadProcessId")
	pGetClassNameW             = user32.NewProc("GetClassNameW")
	pIsWindowVisible           = user32.NewProc("IsWindowVisible")
	pSendMessageTimeoutW       = user32.NewProc("SendMessageTimeoutW")
	pPostMessageW              = user32.NewProc("PostMessageW")
	pGetDlgItem                = user32.NewProc("GetDlgItem")
	pGetProcessId              = kernel32.NewProc("GetProcessId")
	pCreateToolhelp32Snapshot  = kernel32.NewProc("CreateToolhelp32Snapshot")
	pProcess32FirstW           = kernel32.NewProc("Process32FirstW")
	pProcess32NextW            = kernel32.NewProc("Process32NextW")
	pQueryFullProcessImageName = kernel32.NewProc("QueryFullProcessImageNameW")
)

const (
	wmGetText   = 0x000D
	wmCommand   = 0x0111
	idOK        = 1
	idCancel    = 2
	idYes       = 6
	idNo        = 7
	smtoAbort   = 0x0002
	th32Process = 0x2
	procQuery   = 0x1000 // PROCESS_QUERY_LIMITED_INFORMATION
	synchronize = 0x00100000
)

type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hIcon        uintptr
	hProcess     syscall.Handle
}

// shellOpen starts a file the way Explorer does on a double-click (ShellExecuteExW with the default verb), here
// with SEE_MASK_NOCLOSEPROCESS so that the exit code can be read.
func shellOpen(file string) (syscall.Handle, uint32, error) {
	info := shellExecuteInfo{fMask: 0x40 | 0x100, lpFile: u16(file), nShow: 1}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if r, _, err := pShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		return 0, 0, fmt.Errorf("ShellExecuteExW(%s): %v", file, err)
	}
	if info.hProcess == 0 {
		return 0, 0, errors.New("ShellExecuteExW gave no process")
	}
	pid, _, _ := pGetProcessId.Call(uintptr(info.hProcess))
	return info.hProcess, uint32(pid), nil
}

// One callback for each enumeration (syscall.NewCallback has a fixed budget); the state is global, used by one
// goroutine at a time.
var (
	enumPID   uint32
	enumFound []uintptr
	enumTop   = syscall.NewCallback(func(h, _ uintptr) uintptr {
		var pid uint32
		pGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
		if v, _, _ := pIsWindowVisible.Call(h); v != 0 && pid == enumPID && className(h) == "#32770" {
			enumFound = append(enumFound, h)
		}
		return 1
	})
	enumKids = syscall.NewCallback(func(h, _ uintptr) uintptr {
		enumFound = append(enumFound, h)
		return 1
	})
)

func className(h uintptr) string {
	buf := make([]uint16, 64)
	n, _, _ := pGetClassNameW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

// dialogOf returns a visible dialog window of the process, or 0.
func dialogOf(pid uint32) uintptr {
	enumPID, enumFound = pid, nil
	pEnumWindows.Call(enumTop, 0)
	if len(enumFound) == 0 {
		return 0
	}
	return enumFound[0]
}

func windowText(h uintptr) string {
	buf := make([]uint16, 4096)
	var res uintptr
	pSendMessageTimeoutW.Call(h, wmGetText, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])), smtoAbort, 2000, uintptr(unsafe.Pointer(&res)))
	return syscall.UTF16ToString(buf)
}

// dialogText is the title and every child window's text, joined with " | ".
func dialogText(h uintptr) string {
	parts := []string{windowText(h)}
	enumFound = nil
	pEnumChildWindows.Call(h, enumKids, 0)
	for _, k := range append([]uintptr(nil), enumFound...) {
		if t := strings.TrimSpace(windowText(k)); t != "" {
			parts = append(parts, strings.ReplaceAll(t, "\n", " "))
		}
	}
	return strings.Join(parts, " | ")
}

// press presses the button with the given id; false when the dialog has none.
func press(h uintptr, ids ...int) bool {
	for _, id := range ids {
		if b, _, _ := pGetDlgItem.Call(h, uintptr(id)); b != 0 {
			pPostMessageW.Call(h, wmCommand, uintptr(id), b)
			return true
		}
	}
	return false
}

// processes returns the running processes with the given executable name: pid → full path ("" when this user may
// not query it).
func processes(name string) map[uint32]string {
	out := map[uint32]string{}
	snap, _, _ := pCreateToolhelp32Snapshot.Call(th32Process, 0)
	if snap == uintptr(syscall.InvalidHandle) {
		return out
	}
	defer syscall.CloseHandle(syscall.Handle(snap))
	var e struct {
		size, usage, pid      uint32
		heap                  uintptr
		module, threads, ppid uint32
		pri                   int32
		flags                 uint32
		exe                   [260]uint16
	}
	e.size = uint32(unsafe.Sizeof(e))
	for r, _, _ := pProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&e))); r != 0; r, _, _ = pProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&e))) {
		if strings.EqualFold(syscall.UTF16ToString(e.exe[:]), name) {
			out[e.pid] = imagePath(e.pid)
		}
	}
	return out
}

func imagePath(pid uint32) string {
	h, err := syscall.OpenProcess(procQuery, false, pid)
	if err != nil {
		return ""
	}
	defer syscall.CloseHandle(h)
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if r, _, _ := pQueryFullProcessImageName.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n))); r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

func exited(p syscall.Handle) bool {
	r, _ := syscall.WaitForSingleObject(p, 0)
	return r == syscall.WAIT_OBJECT_0
}

func exitCode(p syscall.Handle, wait time.Duration) int {
	if r, _ := syscall.WaitForSingleObject(p, uint32(wait/time.Millisecond)); r != syscall.WAIT_OBJECT_0 {
		return -1
	}
	var code uint32
	if syscall.GetExitCodeProcess(p, &code) != nil {
		return -1
	}
	return int(code)
}

// waitFor polls f every 250 ms until it is true or the time is up.
func waitFor(d time.Duration, f func() bool) bool {
	end := time.Now().Add(d)
	for {
		if f() {
			return true
		}
		if time.Now().After(end) {
			return false
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// Token of this process.

const (
	tokenElevationTypeClass = 18
	elevationDefault        = 1 // a standard user (or UAC off)
	elevationFull           = 2
	elevationLimited        = 3 // an administrator's filtered token
)

func tokenInfo(class uint32) uint32 {
	cur, _ := syscall.GetCurrentProcess()
	var t syscall.Token
	if syscall.OpenProcessToken(cur, syscall.TOKEN_QUERY, &t) != nil {
		return 0
	}
	defer t.Close()
	var v, n uint32
	if syscall.GetTokenInformation(t, class, (*byte)(unsafe.Pointer(&v)), 4, &n) != nil {
		return 0
	}
	return v
}

func elevated() bool        { return tokenInfo(20) != 0 }
func elevationType() uint32 { return tokenInfo(tokenElevationTypeClass) }

func currentUser() (name, sid string) {
	cur, _ := syscall.GetCurrentProcess()
	var t syscall.Token
	if syscall.OpenProcessToken(cur, syscall.TOKEN_QUERY, &t) != nil {
		return "", ""
	}
	defer t.Close()
	u, err := t.GetTokenUser()
	if err != nil {
		return "", ""
	}
	sid, _ = u.User.Sid.String()
	acc, dom, _, _ := u.User.Sid.LookupAccount("")
	return dom + `\` + acc, sid
}

// accountSID looks up a local account's SID.
func accountSID(name string) string {
	s, _, _, err := syscall.LookupSID("", name)
	if err != nil {
		return ""
	}
	str, _ := s.String()
	return str
}

// Foreground. A program started by a task (not by a click in Explorer) does not get the foreground, and the
// elevation prompt of a program in the background opens only as a flashing taskbar button (VM run 1: yk2's first
// sign-in left the Start menu open, the prompt stayed in the taskbar). Before a double-click the checker closes the
// Start menu (Escape), and it brings the installer's question to the foreground before pressing it: a key event of
// this process (Alt) lets SetForegroundWindow succeed (the foreground rules of SetForegroundWindow's documentation:
// the process received the last input event).

var (
	pKeybdEvent         = user32.NewProc("keybd_event")
	pSetForegroundWin   = user32.NewProc("SetForegroundWindow")
	pGetForegroundWin   = user32.NewProc("GetForegroundWindow")
	vkEscape, vkMenu    = uintptr(0x1B), uintptr(0x12)
	keyeventfKeyUp      = uintptr(2)
	startMenuSettleTime = 2 * time.Second
)

func tapKey(vk uintptr) {
	pKeybdEvent.Call(vk, 0, 0, 0)
	pKeybdEvent.Call(vk, 0, keyeventfKeyUp, 0)
}

// closeStart presses Escape (closes the Start menu or a search pane if one is open) and waits for the shell.
func closeStart() {
	tapKey(vkEscape)
	time.Sleep(startMenuSettleTime)
}

// toForeground makes h the foreground window; it reports whether h is the foreground window afterwards.
func toForeground(h uintptr) bool {
	tapKey(vkMenu)
	pSetForegroundWin.Call(h)
	time.Sleep(300 * time.Millisecond)
	f, _, _ := pGetForegroundWin.Call()
	return f == h
}
