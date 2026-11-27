//go:build windows

package winhost

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"github.com/yakbd/yakbd-cli/installer/setup"
)

// Windows API functions (names and constants as in Microsoft's public API documentation). Every DLL is loaded from
// the system directory only (docs/installer.md §5.2): kernel32.dll and advapi32.dll by name, because Go's syscall
// package loads the names it uses itself from System32 only, the others through systemDLL.
var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")
	user32   = systemDLL("user32.dll")
	shell32  = systemDLL("shell32.dll")
	ole32    = systemDLL("ole32.dll")

	pIsWow64Process2           = kernel32.NewProc("IsWow64Process2")
	pIsWow64Process            = kernel32.NewProc("IsWow64Process")
	pGetSystemDirectoryW       = kernel32.NewProc("GetSystemDirectoryW")
	pGetSystemWindowsDirectory = kernel32.NewProc("GetSystemWindowsDirectoryW")
	pGetSystemWow64DirectoryW  = kernel32.NewProc("GetSystemWow64DirectoryW")
	pMoveFileExW               = kernel32.NewProc("MoveFileExW")
	pWow64DisableRedirection   = kernel32.NewProc("Wow64DisableWow64FsRedirection")
	pWow64RevertRedirection    = kernel32.NewProc("Wow64RevertWow64FsRedirection")
	pRegCreateKeyExW           = advapi32.NewProc("RegCreateKeyExW")
	pRegDeleteKeyExW           = advapi32.NewProc("RegDeleteKeyExW")
	pRegSetValueExW            = advapi32.NewProc("RegSetValueExW")
	pRegDeleteValueW           = advapi32.NewProc("RegDeleteValueW")
	pRegEnumValueW             = advapi32.NewProc("RegEnumValueW")
	pGetKeyboardLayoutList     = user32.NewProc("GetKeyboardLayoutList")
	pMessageBoxW               = user32.NewProc("MessageBoxW")
	pShellExecuteExW           = shell32.NewProc("ShellExecuteExW")
	pSHGetKnownFolderPath      = shell32.NewProc("SHGetKnownFolderPath")
	pCoTaskMemFree             = ole32.NewProc("CoTaskMemFree")
)

const (
	hkeyLocalMachine    = 0x80000002
	hkeyUsers           = 0x80000003
	keyWow6464Key       = 0x0100
	keyRead             = 0x20019
	keySetValue         = 0x0002
	keyAllAccess        = 0xF003F
	regCreatedNewKey    = 1
	errFileNotFound     = 2
	errPathNotFound     = 3
	errAccessDenied     = 5
	errSharingViolation = 32
	errMoreData         = 234
	errNoMoreItems      = 259
	errUserMappedFile   = 1224
	errCancelledCode    = 1223
	movefileReplace     = 0x1
	movefileDelay       = 0x4
	tokenElevation      = 20
	seeMaskNoCloseProc  = 0x40
	seeMaskNoAsync      = 0x100
	swShowNormal        = 1
	mbYesNo             = 0x4
	mbIconError         = 0x10
	mbIconQuestion      = 0x20
	mbIconInformation   = 0x40
	idYes               = 6
)

// Host is Windows as the installer programs need it: installer/setup's and installer/toolsetup's System, plus
// elevation, dialogs, the lock and the restart (installer/stub, installer/toolstub).
type Host struct {
	wow bool // a 32-bit process on 64-bit Windows
}

// New returns the host of this process.
func New() *Host {
	h := &Host{}
	if unsafe.Sizeof(uintptr(0)) == 4 {
		var w int32
		cur, _ := syscall.GetCurrentProcess()
		if r, _, _ := pIsWow64Process.Call(uintptr(cur), uintptr(unsafe.Pointer(&w))); r != 0 && w != 0 {
			h.wow = true
		}
	}
	return h
}

func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func dirCall(p *syscall.LazyProc) (string, error) {
	buf := make([]uint16, 512)
	n, _, err := p.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 || int(n) > len(buf) {
		return "", fmt.Errorf("%s: %v", p.Name, err)
	}
	return syscall.UTF16ToString(buf[:n]), nil
}

// NativeMachine takes the machine from IsWow64Process2 (inst §2.2). Without that function (Windows 10 before
// version 1511) the machine is x64 for a WOW64 process and x86 otherwise.
func (h *Host) NativeMachine() (uint16, error) {
	cur, _ := syscall.GetCurrentProcess()
	if pIsWow64Process2.Find() == nil {
		var proc, native uint16
		if r, _, err := pIsWow64Process2.Call(uintptr(cur), uintptr(unsafe.Pointer(&proc)), uintptr(unsafe.Pointer(&native))); r == 0 {
			return 0, fmt.Errorf("IsWow64Process2: %v", err)
		}
		return native, nil
	}
	if h.wow {
		return 0x8664, nil
	}
	return 0x014C, nil
}

func (h *Host) Elevated() bool {
	cur, _ := syscall.GetCurrentProcess()
	var t syscall.Token
	if err := syscall.OpenProcessToken(cur, syscall.TOKEN_QUERY, &t); err != nil {
		return false
	}
	defer t.Close()
	var elevated, n uint32
	if err := syscall.GetTokenInformation(t, tokenElevation, (*byte)(unsafe.Pointer(&elevated)), 4, &n); err != nil {
		return false
	}
	return elevated != 0
}

func (h *Host) UserSID() (string, error) {
	cur, _ := syscall.GetCurrentProcess()
	var t syscall.Token
	if err := syscall.OpenProcessToken(cur, syscall.TOKEN_QUERY, &t); err != nil {
		return "", err
	}
	defer t.Close()
	u, err := t.GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String()
}

// Dir: the native system directory through Sysnative from a WOW64 process, the WOW64 directory by
// GetSystemWow64DirectoryW (inst §2.2).
func (h *Host) Dir(k setup.DirKind) (string, error) {
	if k == setup.DirWOW64 {
		return dirCall(pGetSystemWow64DirectoryW)
	}
	if h.wow {
		w, err := dirCall(pGetSystemWindowsDirectory)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(w, `\`) + `\Sysnative`, nil
	}
	return dirCall(pGetSystemDirectoryW)
}

// ProgramFiles is FOLDERID_ProgramFiles as this process sees it.
func (h *Host) ProgramFiles() (string, error) {
	id := syscall.GUID{Data1: 0x905e63b6, Data2: 0xc1bf, Data3: 0x494e, Data4: [8]byte{0xb2, 0x9c, 0x65, 0xb7, 0x32, 0xd3, 0xd2, 0x1a}}
	var p *uint16
	r, _, _ := pSHGetKnownFolderPath.Call(uintptr(unsafe.Pointer(&id)), 0, 0, uintptr(unsafe.Pointer(&p)))
	if r != 0 || p == nil {
		return "", fmt.Errorf("SHGetKnownFolderPath: %#x", r)
	}
	defer pCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	return utf16PtrToString(p), nil
}

func utf16PtrToString(p *uint16) string {
	var s []uint16
	for q := unsafe.Pointer(p); *(*uint16)(q) != 0; q = unsafe.Add(q, 2) {
		s = append(s, *(*uint16)(q))
	}
	return syscall.UTF16ToString(s)
}

func (h *Host) TempDir() string { return strings.TrimRight(os.TempDir(), `\`) }

func (h *Host) SelfPath() string {
	p, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	return p
}

// fileErr maps Windows errors to setup's.
func fileErr(err error) error {
	if err == nil {
		return nil
	}
	var en syscall.Errno
	if errors.As(err, &en) {
		switch en {
		case errFileNotFound, errPathNotFound:
			return fmt.Errorf("%w (%v)", setup.ErrNotFound, err)
		case errSharingViolation, errAccessDenied, errUserMappedFile:
			return fmt.Errorf("%w (%v)", setup.ErrInUse, err)
		}
	}
	return err
}

func (h *Host) ReadFile(p string) ([]byte, error) {
	b, err := os.ReadFile(p)
	return b, fileErr(err)
}
func (h *Host) WriteFile(p string, d []byte) error { return fileErr(os.WriteFile(p, d, 0o644)) }
func (h *Host) Remove(p string) error              { return fileErr(syscall.DeleteFile(u16(p))) }
func (h *Host) MkdirAll(p string) error            { return os.MkdirAll(p, 0o755) }
func (h *Host) RemoveDir(p string) error           { return syscall.RemoveDirectory(u16(p)) }

func moveFile(from string, to *uint16, flags uintptr) error {
	r, _, err := pMoveFileExW.Call(uintptr(unsafe.Pointer(u16(from))), uintptr(unsafe.Pointer(to)), flags)
	if r == 0 {
		return fileErr(err)
	}
	return nil
}

func (h *Host) Rename(from, to string) error { return moveFile(from, u16(to), movefileReplace) }

// DeleteAtReboot and MoveAtReboot register an operation for the next restart. A path through Sysnative is stored
// through System32 instead, with the thread's file-system redirection off for the call (BootPath).
func (h *Host) DeleteAtReboot(p string) error {
	return atReboot(p, "", movefileDelay)
}
func (h *Host) MoveAtReboot(from, to string) error {
	return atReboot(from, to, movefileDelay|movefileReplace)
}

func atReboot(from, to string, flags uintptr) error {
	windir, err := dirCall(pGetSystemWindowsDirectory)
	if err != nil {
		return err
	}
	f, nf := BootPath(from, windir)
	t, nt := BootPath(to, windir)
	var tp *uint16
	if to != "" {
		tp = u16(t)
	}
	if !nf && !nt {
		return moveFile(f, tp, flags)
	}
	// Redirection is a property of the thread: keep this goroutine on it until it is switched back.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var old uintptr
	if r, _, err := pWow64DisableRedirection.Call(uintptr(unsafe.Pointer(&old))); r == 0 {
		return fmt.Errorf("Wow64DisableWow64FsRedirection: %v", err)
	}
	defer pWow64RevertRedirection.Call(old)
	return moveFile(f, tp, flags)
}

// Registry, always the 64-bit view.

func splitKey(p string) (syscall.Handle, string, error) {
	root, sub, _ := strings.Cut(p, `\`)
	switch strings.ToUpper(root) {
	case "HKLM":
		return hkeyLocalMachine, sub, nil
	case "HKU":
		return hkeyUsers, sub, nil
	}
	return 0, "", fmt.Errorf("unknown registry root in %s", p)
}

func openKey(p string, access uint32) (syscall.Handle, error) {
	root, sub, err := splitKey(p)
	if err != nil {
		return 0, err
	}
	var k syscall.Handle
	if err := syscall.RegOpenKeyEx(root, u16(sub), 0, access|keyWow6464Key, &k); err != nil {
		if err == syscall.Errno(errFileNotFound) {
			return 0, setup.ErrNotFound
		}
		return 0, err
	}
	return k, nil
}

func (h *Host) CreateKey(p string) (bool, error) {
	root, sub, err := splitKey(p)
	if err != nil {
		return false, err
	}
	var k syscall.Handle
	var disp uint32
	r, _, _ := pRegCreateKeyExW.Call(uintptr(root), uintptr(unsafe.Pointer(u16(sub))), 0, 0, 0, keyAllAccess|keyWow6464Key, 0,
		uintptr(unsafe.Pointer(&k)), uintptr(unsafe.Pointer(&disp)))
	if r != 0 {
		return false, syscall.Errno(r)
	}
	syscall.RegCloseKey(k)
	return disp == regCreatedNewKey, nil
}

func (h *Host) DeleteKey(p string) error {
	root, sub, err := splitKey(p)
	if err != nil {
		return err
	}
	r, _, _ := pRegDeleteKeyExW.Call(uintptr(root), uintptr(unsafe.Pointer(u16(sub))), keyWow6464Key, 0)
	if r != 0 && r != errFileNotFound {
		return syscall.Errno(r)
	}
	return nil
}

func (h *Host) SubKeys(p string) ([]string, error) {
	k, err := openKey(p, keyRead)
	if err != nil {
		return nil, err
	}
	defer syscall.RegCloseKey(k)
	var out []string
	for i := uint32(0); ; i++ {
		buf := make([]uint16, 512)
		n := uint32(len(buf))
		err := syscall.RegEnumKeyEx(k, i, &buf[0], &n, nil, nil, nil, nil)
		if err == syscall.Errno(errNoMoreItems) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, syscall.UTF16ToString(buf[:n]))
	}
}

func (h *Host) Values(p string) ([]setup.Value, error) {
	k, err := openKey(p, keyRead)
	if err != nil {
		return nil, err
	}
	defer syscall.RegCloseKey(k)
	var out []setup.Value
	for i := uint32(0); ; i++ {
		name := make([]uint16, 16384)
		data := make([]byte, 65536)
		nn, dn := uint32(len(name)), uint32(len(data))
		var typ uint32
		r, _, _ := pRegEnumValueW.Call(uintptr(k), uintptr(i), uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&nn)), 0,
			uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&dn)))
		if r == errNoMoreItems {
			return out, nil
		}
		if r == errMoreData {
			continue // a value larger than 64 KiB is nothing the installer reads
		}
		if r != 0 {
			return out, syscall.Errno(r)
		}
		v := setup.Value{Name: syscall.UTF16ToString(name[:nn]), Type: typ}
		switch typ {
		case setup.RegSZ, setup.RegExpandSZ, setup.RegMultiSZ:
			u := make([]uint16, dn/2)
			for j := range u {
				u[j] = uint16(data[2*j]) | uint16(data[2*j+1])<<8
			}
			if typ == setup.RegMultiSZ {
				v.Str = strings.TrimRight(string(utf16.Decode(u)), "\x00") // the strings joined by NUL
			} else {
				v.Str = syscall.UTF16ToString(u)
			}
		case setup.RegDWORD:
			if dn >= 4 {
				v.DWord = uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24
			}
		}
		out = append(out, v)
	}
}

func (h *Host) setValue(p, name string, typ uint32, data unsafe.Pointer, size int) error {
	k, err := openKey(p, keySetValue)
	if err != nil {
		return err
	}
	defer syscall.RegCloseKey(k)
	r, _, _ := pRegSetValueExW.Call(uintptr(k), uintptr(unsafe.Pointer(u16(name))), 0, uintptr(typ), uintptr(data), uintptr(size))
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

func (h *Host) SetString(p, name, value string, expand bool) error {
	typ := setup.RegSZ
	if expand {
		typ = setup.RegExpandSZ
	}
	d, _ := syscall.UTF16FromString(value)
	return h.setValue(p, name, typ, unsafe.Pointer(&d[0]), 2*len(d))
}

func (h *Host) SetDWORD(p, name string, v uint32) error {
	return h.setValue(p, name, setup.RegDWORD, unsafe.Pointer(&v), 4)
}

func (h *Host) DeleteValue(p, name string) error {
	k, err := openKey(p, keySetValue)
	if err != nil {
		return err
	}
	defer syscall.RegCloseKey(k)
	r, _, _ := pRegDeleteValueW.Call(uintptr(k), uintptr(unsafe.Pointer(u16(name))))
	if r == errFileNotFound {
		return setup.ErrNotFound
	}
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

// input.dll has no import library (inst §4.3): loaded from the system directory by full path.
func inputProc(name string) (*syscall.LazyProc, error) {
	dir, err := dirCall(pGetSystemDirectoryW)
	if err != nil {
		return nil, err
	}
	p := syscall.NewLazyDLL(dir + `\input.dll`).NewProc(name)
	if err := p.Find(); err != nil {
		return nil, err
	}
	return p, nil
}

func (h *Host) InstallLayoutOrTip(s string, flags uint32) error {
	p, err := inputProc("InstallLayoutOrTip")
	if err != nil {
		return err
	}
	if r, _, err := p.Call(uintptr(unsafe.Pointer(u16(s))), uintptr(flags)); r == 0 {
		return fmt.Errorf("FALSE (%v)", err)
	}
	return nil
}

func (h *Host) InstallLayoutOrTipUserReg(hive, s string, flags uint32) error {
	p, err := inputProc("InstallLayoutOrTipUserReg")
	if err != nil {
		return err
	}
	if r, _, err := p.Call(uintptr(unsafe.Pointer(u16(hive))), 0, 0, uintptr(unsafe.Pointer(u16(s))), uintptr(flags)); r == 0 {
		return fmt.Errorf("FALSE (%v)", err)
	}
	return nil
}

func (h *Host) LayoutList() ([]uint32, error) {
	n, _, _ := pGetKeyboardLayoutList.Call(0, 0)
	if n == 0 {
		return nil, nil
	}
	buf := make([]uintptr, n)
	n, _, err := pGetKeyboardLayoutList.Call(n, uintptr(unsafe.Pointer(&buf[0])))
	if n == 0 {
		return nil, fmt.Errorf("GetKeyboardLayoutList: %v", err)
	}
	out := make([]uint32, n)
	for i := range out {
		out[i] = uint32(buf[i])
	}
	return out, nil
}

// shellExecuteInfo is SHELLEXECUTEINFOW.
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

func (h *Host) Relaunch(args []string) (int, error) {
	var q []string
	for _, a := range args {
		q = append(q, syscall.EscapeArg(a))
	}
	info := shellExecuteInfo{fMask: seeMaskNoCloseProc | seeMaskNoAsync, lpVerb: u16("runas"), lpFile: u16(h.SelfPath()),
		lpParameters: u16(strings.Join(q, " ")), nShow: swShowNormal}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if r, _, err := pShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		if err == syscall.Errno(errCancelledCode) {
			return 0, ErrCancelled
		}
		return 0, fmt.Errorf("ShellExecuteExW: %v", err)
	}
	if info.hProcess == 0 {
		return 0, errors.New("ShellExecuteExW gave no process")
	}
	defer syscall.CloseHandle(info.hProcess)
	if _, err := syscall.WaitForSingleObject(info.hProcess, syscall.INFINITE); err != nil {
		return 0, err
	}
	var code uint32
	if err := syscall.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return 0, err
	}
	return int(code), nil
}

func (h *Host) Message(title, text string, kind MsgKind) bool {
	flags := uintptr(mbIconInformation)
	switch kind {
	case MsgError:
		flags = mbIconError
	case MsgQuestion:
		flags = mbYesNo | mbIconQuestion
	}
	r, _, _ := pMessageBoxW.Call(0, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16(title))), flags)
	return r == idYes
}

var (
	wtsapi32                     = systemDLL("wtsapi32.dll")
	pWTSQuerySessionInformationW = wtsapi32.NewProc("WTSQuerySessionInformationW")
	pWTSFreeMemory               = wtsapi32.NewProc("WTSFreeMemory")
)

func sessionInfo(class uintptr) (string, error) {
	var p *uint16
	var n uint32
	r, _, err := pWTSQuerySessionInformationW.Call(0, 0xFFFFFFFF, class, uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&n)))
	if r == 0 || p == nil {
		return "", fmt.Errorf("WTSQuerySessionInformationW: %v", err)
	}
	defer pWTSFreeMemory.Call(uintptr(unsafe.Pointer(p)))
	return utf16PtrToString(p), nil
}

// SessionUser compares this process's account with the user of its session (WTSUserName, WTSDomainName).
func (h *Host) SessionUser() (bool, error) {
	user, err := sessionInfo(5)
	if err != nil {
		return false, err
	}
	domain, _ := sessionInfo(7)
	cur, _ := syscall.GetCurrentProcess()
	var t syscall.Token
	if err := syscall.OpenProcessToken(cur, syscall.TOKEN_QUERY, &t); err != nil {
		return false, err
	}
	defer t.Close()
	u, err := t.GetTokenUser()
	if err != nil {
		return false, err
	}
	acc, dom, _, err := u.User.Sid.LookupAccount("")
	if err != nil {
		return false, err
	}
	return strings.EqualFold(acc, user) && (domain == "" || strings.EqualFold(dom, domain)), nil
}

func (h *Host) SetMultiString(p, name string, values []string) error {
	var d []uint16
	for _, v := range values {
		u, _ := syscall.UTF16FromString(v)
		d = append(d, u...)
	}
	d = append(d, 0)
	if len(values) == 0 {
		d = append(d, 0)
	}
	return h.setValue(p, name, setup.RegMultiSZ, unsafe.Pointer(&d[0]), 2*len(d))
}

// powershell runs a script in the native Windows PowerShell as this process's user.
func (h *Host) powershell(script string) error {
	dir, err := h.Dir(setup.DirNative)
	if err != nil {
		return err
	}
	cmd := exec.Command(dir+`\WindowsPowerShell\v1.0\powershell.exe`, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// AddLanguageTag adds a tag to the user's language list, reading the list first (inst section 4.4: never a new list).
// The tag is checked by setup (letters, digits and hyphens only).
func (h *Host) AddLanguageTag(tag string) error {
	return h.powershell(`$ErrorActionPreference = 'Stop'; $l = Get-WinUserLanguageList; $l.Add('` + tag + `'); Set-WinUserLanguageList $l -Force`)
}

func (h *Host) RemoveLanguageTag(tag string) error {
	return h.powershell(`$ErrorActionPreference = 'Stop'; $l = Get-WinUserLanguageList; foreach ($i in @($l | Where-Object { $_.LanguageTag -eq '` + tag + `' })) { [void]$l.Remove($i) }; Set-WinUserLanguageList $l -Force`)
}

var (
	pCreateMutexW          = kernel32.NewProc("CreateMutexW")
	pInitiateShutdownW     = advapi32.NewProc("InitiateShutdownW")
	pLookupPrivilegeValueW = advapi32.NewProc("LookupPrivilegeValueW")
	pAdjustTokenPrivileges = advapi32.NewProc("AdjustTokenPrivileges")
	lockHandle             uintptr
)

// LockName is the machine-wide mutex every yakbd installer holds while it changes the machine (docs/installer-cli.md
// §8). Windows Installer's own _MSIExecute mutex is not used: its namespace is not documented, and these
// installers do not run in the Windows Installer service.
const LockName = `Global\YAKBD-Setup`

func (h *Host) Lock() (bool, error) {
	m, _, err := pCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(u16(LockName))))
	if m == 0 {
		return false, fmt.Errorf("CreateMutexW: %v", err)
	}
	ev, err := syscall.WaitForSingleObject(syscall.Handle(m), 0)
	switch ev {
	case syscall.WAIT_OBJECT_0, 0x80: // WAIT_ABANDONED: its last owner ended without releasing it
		lockHandle = m
		return true, nil
	case 0x102: // WAIT_TIMEOUT
		syscall.CloseHandle(syscall.Handle(m))
		return false, nil
	}
	syscall.CloseHandle(syscall.Handle(m))
	return false, fmt.Errorf("WaitForSingleObject: %v", err)
}

// Restart enables SeShutdownPrivilege and calls InitiateShutdownW(NULL, message, 0, SHUTDOWN_RESTART,
// SHTDN_REASON_MAJOR_APPLICATION | SHTDN_REASON_MINOR_INSTALLATION | SHTDN_REASON_FLAG_PLANNED)
// (https://learn.microsoft.com/en-us/windows/win32/api/winreg/nf-winreg-initiateshutdownw). Without
// SHUTDOWN_FORCE_OTHERS it fails when other users are signed in; the caller then reports 3010.
func (h *Host) Restart() error {
	cur, _ := syscall.GetCurrentProcess()
	var t syscall.Token
	if err := syscall.OpenProcessToken(cur, syscall.TOKEN_ADJUST_PRIVILEGES|syscall.TOKEN_QUERY, &t); err != nil {
		return err
	}
	defer t.Close()
	var tp struct {
		count uint32
		luid  [2]uint32
		attr  uint32
	}
	if r, _, err := pLookupPrivilegeValueW.Call(0, uintptr(unsafe.Pointer(u16("SeShutdownPrivilege"))), uintptr(unsafe.Pointer(&tp.luid))); r == 0 {
		return fmt.Errorf("LookupPrivilegeValueW: %v", err)
	}
	tp.count, tp.attr = 1, 2 // SE_PRIVILEGE_ENABLED
	if r, _, err := pAdjustTokenPrivileges.Call(uintptr(t), 0, uintptr(unsafe.Pointer(&tp)), 0, 0, 0); r == 0 {
		return fmt.Errorf("AdjustTokenPrivileges: %v", err)
	}
	const shutdownRestart, reason = 0x4, 0x80040002
	r, _, _ := pInitiateShutdownW.Call(0, uintptr(unsafe.Pointer(u16("A keyboard layout installer restarts Windows to finish."))), 0, shutdownRestart, reason)
	if r != 0 {
		return fmt.Errorf("InitiateShutdownW: %v", syscall.Errno(r))
	}
	return nil
}

// NativeProgramFiles is the native Program Files folder: %ProgramW6432% for a 32-bit process on 64-bit Windows
// (WOW64 Implementation Details: a 32-bit process sees ProgramFiles as the x86 folder and ProgramW6432 as the
// native one, https://learn.microsoft.com/en-us/windows/win32/winprog64/wow64-implementation-details), else
// FOLDERID_ProgramFiles.
func (h *Host) NativeProgramFiles() (string, error) {
	if h.wow {
		if p := os.Getenv("ProgramW6432"); p != "" {
			return p, nil
		}
		return "", errors.New("ProgramW6432 is not set")
	}
	return h.ProgramFiles()
}

var pSendMessageTimeoutW = user32.NewProc("SendMessageTimeoutW")

// BroadcastEnvironment tells running programs that the system environment changed: WM_SETTINGCHANGE (0x001A) with
// lParam "Environment" to HWND_BROADCAST by SendMessageTimeout (SMTO_ABORTIFHUNG, 5 s)
// (https://learn.microsoft.com/en-us/windows/win32/procthread/environment-variables,
// https://learn.microsoft.com/en-us/windows/win32/winmsg/wm-settingchange).
func (h *Host) BroadcastEnvironment() error {
	const hwndBroadcast, wmSettingChange, smtoAbortIfHung = 0xFFFF, 0x001A, 0x0002
	var res uintptr
	r, _, err := pSendMessageTimeoutW.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(u16("Environment"))),
		smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&res)))
	if r == 0 {
		return fmt.Errorf("SendMessageTimeoutW: %v", err)
	}
	return nil
}

var (
	userenv                         = systemDLL("userenv.dll")
	pGetDefaultUserProfileDirectory = userenv.NewProc("GetDefaultUserProfileDirectoryW")
	pRegLoadKeyW                    = advapi32.NewProc("RegLoadKeyW")
	pRegUnLoadKeyW                  = advapi32.NewProc("RegUnLoadKeyW")
)

// DefaultProfileHive is NTUSER.DAT in the folder GetDefaultUserProfileDirectoryW names, the profile new accounts
// are made from (https://learn.microsoft.com/en-us/windows/win32/api/userenv/nf-userenv-getdefaultuserprofiledirectoryw;
// inst §4.7: C:\Users\Default\NTUSER.DAT).
func (h *Host) DefaultProfileHive() (string, error) {
	n := uint32(0)
	pGetDefaultUserProfileDirectory.Call(0, uintptr(unsafe.Pointer(&n)))
	if n == 0 || n > 32768 {
		return "", fmt.Errorf("GetDefaultUserProfileDirectoryW: no size")
	}
	buf := make([]uint16, n)
	if r, _, err := pGetDefaultUserProfileDirectory.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n))); r == 0 {
		return "", fmt.Errorf("GetDefaultUserProfileDirectoryW: %v", err)
	}
	return strings.TrimRight(syscall.UTF16ToString(buf), `\`) + `\NTUSER.DAT`, nil
}

// enablePrivileges enables privileges in this process's token (AdjustTokenPrivileges); it fails when the token
// lacks one of them.
func enablePrivileges(names ...string) error {
	cur, _ := syscall.GetCurrentProcess()
	var t syscall.Token
	if err := syscall.OpenProcessToken(cur, syscall.TOKEN_ADJUST_PRIVILEGES|syscall.TOKEN_QUERY, &t); err != nil {
		return err
	}
	defer t.Close()
	for _, name := range names {
		var tp struct {
			count uint32
			luid  [2]uint32
			attr  uint32
		}
		if r, _, err := pLookupPrivilegeValueW.Call(0, uintptr(unsafe.Pointer(u16(name))), uintptr(unsafe.Pointer(&tp.luid))); r == 0 {
			return fmt.Errorf("LookupPrivilegeValueW(%s): %v", name, err)
		}
		tp.count, tp.attr = 1, 2 // SE_PRIVILEGE_ENABLED
		r, _, err := pAdjustTokenPrivileges.Call(uintptr(t), 0, uintptr(unsafe.Pointer(&tp)), 0, 0, 0)
		if r == 0 {
			return fmt.Errorf("AdjustTokenPrivileges(%s): %v", name, err)
		}
		if err == syscall.Errno(1300) { // ERROR_NOT_ALL_ASSIGNED
			return fmt.Errorf("the token does not hold %s", name)
		}
	}
	return nil
}

// LoadHive loads a hive file as HKU\<name> (RegLoadKeyW, which needs SE_RESTORE_NAME and SE_BACKUP_NAME, the
// privileges "SeRestorePrivilege" and "SeBackupPrivilege" (https://learn.microsoft.com/en-us/windows/win32/secauthz/privilege-constants):
// https://learn.microsoft.com/en-us/windows/win32/api/winreg/nf-winreg-regloadkeyw).
func (h *Host) LoadHive(name, file string) error {
	// RegLoadKeyW creates the file when it does not exist; a missing Default profile must stay missing.
	if _, err := os.Stat(file); err != nil {
		return err
	}
	if err := enablePrivileges("SeRestorePrivilege", "SeBackupPrivilege"); err != nil {
		return err
	}
	if r, _, _ := pRegLoadKeyW.Call(uintptr(hkeyUsers), uintptr(unsafe.Pointer(u16(name))), uintptr(unsafe.Pointer(u16(file)))); r != 0 {
		return fmt.Errorf("RegLoadKeyW: %v", syscall.Errno(r))
	}
	return nil
}

// UnloadHive unloads HKU\<name> (RegUnLoadKeyW,
// https://learn.microsoft.com/en-us/windows/win32/api/winreg/nf-winreg-regunloadkeyw).
func (h *Host) UnloadHive(name string) error {
	if err := enablePrivileges("SeRestorePrivilege", "SeBackupPrivilege"); err != nil {
		return err
	}
	if r, _, _ := pRegUnLoadKeyW.Call(uintptr(hkeyUsers), uintptr(unsafe.Pointer(u16(name)))); r != 0 {
		return fmt.Errorf("RegUnLoadKeyW: %v", syscall.Errno(r))
	}
	return nil
}

// DLL planting (docs/installer.md §5.2). The installer usually runs from a Downloads folder, where anyone's files
// may lie next to it. A DLL named without a path is searched for in the program's own folder before the system
// folder unless it is a Known DLL, and so are the dependencies of every DLL loaded later
// (https://learn.microsoft.com/en-us/windows/win32/dlls/dynamic-link-library-search-order, read 2026-11-27).

// loadLibrarySearchSystem32 is LOAD_LIBRARY_SEARCH_SYSTEM32
// (https://learn.microsoft.com/en-us/windows/win32/api/libloaderapi/nf-libloaderapi-setdefaultdlldirectories).
const loadLibrarySearchSystem32 = 0x00000800

func init() {
	// The process's default search path becomes the system directory alone, for later loads and (as the page says
	// for dependents of a DLL loaded by full path; measured by instbundle -guest w11plant) their dependencies; the
	// program's folder is no longer searched (Windows 8 and later; the call is absent before).
	if p := kernel32.NewProc("SetDefaultDllDirectories"); p.Find() == nil {
		p.Call(loadLibrarySearchSystem32)
	}
}

// systemDLL is a DLL of the system directory, loaded by its full path.
func systemDLL(name string) *syscall.LazyDLL {
	dir, err := dirCall(pGetSystemDirectoryW)
	if err != nil {
		// Without the system directory the name stays bare; the default search path of init still excludes
		// the program's folder.
		return syscall.NewLazyDLL(name)
	}
	return syscall.NewLazyDLL(dir + `\` + name)
}
