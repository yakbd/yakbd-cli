//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/yakbd/yakbd-cli/internal/wincheck"
)

const mapvkVSCToVKEx = 3

// winKeyboard is a loaded layout for the import check.
type winKeyboard struct{ hkl uintptr }

func (s *winSystem) Keyboard(hkl uintptr) wincheck.Keyboard { return winKeyboard{hkl} }

// Translate calls ToUnicodeEx with the given key state and the scan code the layout gives the key.
func (k winKeyboard) Translate(vk byte, state *[256]byte, flags uint32) (int, []uint16) {
	sc, _, _ := pMapVirtualKeyExW.Call(uintptr(vk), mapvkVKToVSC, k.hkl)
	var buf [64]uint16
	r, _, _ := pToUnicodeEx.Call(uintptr(vk), sc, uintptr(unsafe.Pointer(&state[0])),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(flags), k.hkl)
	rc := int(int32(r))
	switch {
	case rc < 0 && buf[0] == 0:
		return rc, nil
	case rc < 0:
		return rc, buf[:1]
	case rc > len(buf):
		rc = len(buf)
	}
	return rc, append([]uint16(nil), buf[:rc]...)
}

func (k winKeyboard) ScanToVK(sc uint16) uint32 {
	v, _, _ := pMapVirtualKeyExW.Call(uintptr(sc), mapvkVSCToVKEx, k.hkl)
	return uint32(v)
}

// LayoutFiles reads kbd*.dll of the system directory as this process sees it (a 32-bit process is redirected
// to SysWOW64). The files are read as data, never loaded from there.
func (s *winSystem) LayoutFiles() (map[string][]byte, error) {
	// Names are matched without regard to case: System32 has both KBDUS.DLL and kbdfr.dll spellings.
	ents, err := os.ReadDir(s.sysDir)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, e := range ents {
		n := strings.ToLower(e.Name())
		if e.IsDir() || !strings.HasPrefix(n, "kbd") || !strings.HasSuffix(n, ".dll") || strings.HasPrefix(n, "ykt") {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(s.sysDir, e.Name())); err == nil {
			out[e.Name()] = b
		}
	}
	return out, nil
}
