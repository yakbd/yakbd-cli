//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"

	"github.com/yakbd/yakbd-cli/installer/toolsetup"
	"github.com/yakbd/yakbd-cli/installer/winhost"
)

// win is Windows for the installers: winhost's Host, the starting user's side and the wizard.
type win struct{ *winhost.Host }

// The starting user's settings (docs/installer-cli.md §12.3): HKEY_CURRENT_USER and the user's environment of this
// process, which is the first, unelevated one when the settings are removed. Microsoft Learn: RegDeleteTreeW,
// CoInitializeEx (for ShellExecuteExW on the wizard's work thread); the other signatures from the windows crate.
var (
	advapi32u        = syscall.NewLazyDLL("advapi32.dll")
	ole32u           = syscall.NewLazyDLL("ole32.dll")
	pRegOpenKeyExW   = advapi32u.NewProc("RegOpenKeyExW")
	pRegQueryInfoKey = advapi32u.NewProc("RegQueryInfoKeyW")
	pRegDeleteTreeW  = advapi32u.NewProc("RegDeleteTreeW")
	pRegCloseKey     = advapi32u.NewProc("RegCloseKey")
	pCoInitializeEx  = ole32u.NewProc("CoInitializeEx")
)

const (
	hkeyCurrentUser = 0x80000001
	keyReadU        = 0x20019
)

func coInit() { pCoInitializeEx.Call(0, 0x2|0x4) } // COINIT_APARTMENTTHREADED | COINIT_DISABLE_OLE1DDE

func (h *win) User() toolsetup.UserSystem { return user{} }

type user struct{}

func (user) Getenv(n string) string { return os.Getenv(n) }

func openUser(p string) (syscall.Handle, bool) {
	var k syscall.Handle
	r, _, _ := pRegOpenKeyExW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(w16(p))), 0, keyReadU, uintptr(unsafe.Pointer(&k)))
	return k, r == 0
}

func (user) UserKeyExists(p string) bool {
	k, ok := openUser(p)
	if ok {
		pRegCloseKey.Call(uintptr(k))
	}
	return ok
}

func (user) UserKeyEmpty(p string) bool {
	k, ok := openUser(p)
	if !ok {
		return false
	}
	defer pRegCloseKey.Call(uintptr(k))
	var subkeys, vals uint32
	r, _, _ := pRegQueryInfoKey.Call(uintptr(k), 0, 0, 0, uintptr(unsafe.Pointer(&subkeys)), 0, 0, uintptr(unsafe.Pointer(&vals)), 0, 0, 0, 0)
	return r == 0 && subkeys == 0 && vals == 0
}

func (user) DeleteUserKeyTree(p string) error {
	if r, _, _ := pRegDeleteTreeW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(w16(p)))); r != 0 {
		return syscall.Errno(r)
	}
	// RegDeleteTreeW deletes the key's subkeys and values (its page does not say whether the named key itself goes);
	// RegDeleteKeyW then deletes the key, and its absence counts as done.
	return deleteKey(p)
}

var pRegDeleteKeyW = advapi32u.NewProc("RegDeleteKeyW")

func deleteKey(p string) error {
	// ERROR_FILE_NOT_FOUND (2): RegDeleteTreeW took the key itself already.
	if r, _, _ := pRegDeleteKeyW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(w16(p)))); r != 0 && r != 2 {
		return syscall.Errno(r)
	}
	return nil
}

func (user) DirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func (user) RemoveTree(p string) error { return os.RemoveAll(p) }
func (user) RemoveDir(p string) error  { return syscall.RemoveDirectory(w16(p)) }
