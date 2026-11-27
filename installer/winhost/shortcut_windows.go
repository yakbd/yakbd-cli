//go:build windows

package winhost

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// Start-menu shortcuts (docs/installer-cli.md §12.2). The function, method and parameter descriptions are from
// Microsoft Learn: "Shell Links", IShellLinkW (its method list), IShellLinkW::SetIconLocation and GetPath,
// IPersistFile::Save and Load, CoInitializeEx (with CoUninitialize), CoCreateInstance, SHGetKnownFolderPath and KNOWNFOLDERID
// (FOLDERID_CommonPrograms). Learn lists an interface's methods by name only; the order of the
// methods in each interface's table of functions (its vtable) and the CLSID and IIDs are from Microsoft's `windows`
// crate 0.61.3 and `windows-core` 0.61.2 (NOTICE): IUnknown QueryInterface 0, AddRef 1, Release 2; IShellLinkW
// GetPath 3, SetDescription 7, GetWorkingDirectory 8, SetWorkingDirectory 9, SetIconLocation 17, SetPath 20;
// IPersistFile (after IPersist's GetClassID 3) Load 5, Save 6; STGM_READ 0, MAX_PATH 260.
var (
	pCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	pCoUninitialize   = ole32.NewProc("CoUninitialize")
	pCoCreateInstance = ole32.NewProc("CoCreateInstance")

	clsidShellLink         = syscall.GUID{Data1: 0x00021401, Data2: 0, Data3: 0, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidShellLinkW          = syscall.GUID{Data1: 0x000214f9, Data2: 0, Data3: 0, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidPersistFile         = syscall.GUID{Data1: 0x0000010b, Data2: 0, Data3: 0, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	folderidCommonPrograms = syscall.GUID{Data1: 0x0139d44e, Data2: 0x6afe, Data3: 0x49f2, Data4: [8]byte{0x86, 0x90, 0x3d, 0xaf, 0xca, 0xe6, 0xff, 0xb8}}
)

const (
	coinitApartmentThreaded = 0x2
	coinitDisableOLE1DDE    = 0x4
	clsctxInprocServer      = 0x1
	rpcEChangedMode         = 0x80010106

	vtQueryInterface      = 0
	vtRelease             = 2
	vtSetDescription      = 7
	vtSetWorkingDirectory = 9
	vtSetIconLocation     = 17
	vtSetPath             = 20
	vtSave                = 6
	vtLoad                = 5
	vtGetPath             = 3
	vtGetWorkingDirectory = 8
	stgmRead              = 0
	maxPath               = 260
)

// CommonPrograms is FOLDERID_CommonPrograms, the Start menu's Programs folder for all users
// (%ALLUSERSPROFILE%\Microsoft\Windows\Start Menu\Programs by default).
func (h *Host) CommonPrograms() (string, error) {
	var p *uint16
	r, _, _ := pSHGetKnownFolderPath.Call(uintptr(unsafe.Pointer(&folderidCommonPrograms)), 0, 0, uintptr(unsafe.Pointer(&p)))
	if p != nil {
		defer pCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	}
	if r != 0 || p == nil {
		return "", fmt.Errorf("SHGetKnownFolderPath(FOLDERID_CommonPrograms): %#x", r)
	}
	return utf16PtrToString(p), nil
}

// comCall calls method i of the COM interface pointer obj and returns its HRESULT. Like syscall's LazyProc.Call,
// it keeps pointers passed as uintptr arguments on the heap and alive for the call.
//
//go:uintptrescapes
func comCall(obj unsafe.Pointer, i int, args ...uintptr) uint32 {
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(i)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	return uint32(r)
}

func failed(hr uint32) bool { return int32(hr) < 0 }

// withShellLink makes a Shell link object (CoCreateInstance of CLSID_ShellLink for IShellLinkW, in process) and its
// IPersistFile, and calls fn with both. COM is initialized for this thread as a single-threaded apartment for the
// call; the goroutine stays on the thread until it is uninitialized.
func withShellLink(fn func(link, pf unsafe.Pointer) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := pCoInitializeEx.Call(0, coinitApartmentThreaded|coinitDisableOLE1DDE)
	switch {
	case uint32(hr) == rpcEChangedMode:
		// COM is already initialized on this thread in another model; use it as it is.
	case failed(uint32(hr)):
		return fmt.Errorf("CoInitializeEx: %#x", uint32(hr))
	default: // S_OK or S_FALSE: balanced by CoUninitialize
		defer pCoUninitialize.Call()
	}
	var link unsafe.Pointer
	r, _, _ := pCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidShellLink)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidShellLinkW)), uintptr(unsafe.Pointer(&link)))
	if failed(uint32(r)) || link == nil {
		return fmt.Errorf("CoCreateInstance(CLSID_ShellLink): %#x", uint32(r))
	}
	defer comCall(link, vtRelease)
	var pf unsafe.Pointer
	if hr := comCall(link, vtQueryInterface, uintptr(unsafe.Pointer(&iidPersistFile)), uintptr(unsafe.Pointer(&pf))); failed(hr) || pf == nil {
		return fmt.Errorf("QueryInterface(IPersistFile): %#x", hr)
	}
	defer comCall(pf, vtRelease)
	return fn(link, pf)
}

// CreateShortcut sets a new Shell link object's target, working folder, icon (the target's first) and description
// (the shortcut's file name without .lnk), and saves it as lnk through IPersistFile::Save (installer/toolsetup deletes
// an earlier file first). Only S_OK counts: Save returns S_FALSE when the object was not saved.
func (h *Host) CreateShortcut(lnk, target, dir string) error {
	name := lnk[lastSep(lnk)+1:]
	if len(name) > 4 && (name[len(name)-4:] == ".lnk" || name[len(name)-4:] == ".LNK") {
		name = name[:len(name)-4]
	}
	pTarget, pDir, pName, pLnk := u16(target), u16(dir), u16(name), u16(lnk)
	defer runtime.KeepAlive([]*uint16{pTarget, pDir, pName, pLnk})
	return withShellLink(func(link, pf unsafe.Pointer) error {
		steps := []struct {
			what string
			i    int
			args []uintptr
		}{
			{"SetPath", vtSetPath, []uintptr{uintptr(unsafe.Pointer(pTarget))}},
			{"SetWorkingDirectory", vtSetWorkingDirectory, []uintptr{uintptr(unsafe.Pointer(pDir))}},
			{"SetIconLocation", vtSetIconLocation, []uintptr{uintptr(unsafe.Pointer(pTarget)), 0}},
			{"SetDescription", vtSetDescription, []uintptr{uintptr(unsafe.Pointer(pName))}},
		}
		for _, s := range steps {
			if hr := comCall(link, s.i, s.args...); failed(hr) {
				return fmt.Errorf("IShellLinkW::%s: %#x", s.what, hr)
			}
		}
		if hr := comCall(pf, vtSave, uintptr(unsafe.Pointer(pLnk)), 1); hr != 0 {
			return fmt.Errorf("IPersistFile::Save(%s): %#x (S_FALSE: not saved)", lnk, hr)
		}
		return nil
	})
}

// ReadShortcut loads the shortcut file lnk into a new Shell link object (IPersistFile::Load, STGM_READ) and returns
// its target (IShellLinkW::GetPath with no flags: neither the 8.3 nor the raw form; S_FALSE, no path, is an error)
// and working folder (GetWorkingDirectory). Paths are at most MAX_PATH (260) characters, as GetPath states.
func (h *Host) ReadShortcut(lnk string) (target, dir string, err error) {
	pLnk := u16(lnk)
	defer runtime.KeepAlive(pLnk)
	err = withShellLink(func(link, pf unsafe.Pointer) error {
		if hr := comCall(pf, vtLoad, uintptr(unsafe.Pointer(pLnk)), stgmRead); hr != 0 {
			return fmt.Errorf("IPersistFile::Load(%s): %#x", lnk, hr)
		}
		buf := make([]uint16, maxPath+1)
		if hr := comCall(link, vtGetPath, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, 0); hr != 0 {
			return fmt.Errorf("IShellLinkW::GetPath: %#x (S_FALSE: no path)", hr)
		}
		target = syscall.UTF16ToString(buf)
		buf = make([]uint16, maxPath+1)
		if hr := comCall(link, vtGetWorkingDirectory, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))); failed(hr) {
			return fmt.Errorf("IShellLinkW::GetWorkingDirectory: %#x", hr)
		}
		dir = syscall.UTF16ToString(buf)
		return nil
	})
	return target, dir, err
}

func lastSep(p string) int {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '\\' || p[i] == '/' {
			return i
		}
	}
	return -1
}
