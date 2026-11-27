// Package winhost is the Windows side of the installer programs (installer/stub for layouts, installer/toolstub
// for yakbd-setup.exe): Host implements installer/setup's and installer/toolsetup's System with the Windows API
// (plain syscall, standard library only), plus elevation, dialogs, the machine-wide lock and the restart
// (docs/installer.md §2, §3; docs/installer-cli.md §5, §8). Only this file builds on other systems: the dialog
// kinds and the cancel error the programs' portable code uses.
package winhost

import (
	"errors"
	"strings"
)

// MsgKind is the kind of a dialog.
type MsgKind int

// The dialog kinds: information, error, and a Yes/No question.
const (
	MsgInfo MsgKind = iota
	MsgError
	MsgQuestion
)

// ErrCancelled is Relaunch's error when the user declined the elevation prompt.
var ErrCancelled = errors.New("cancelled")

// BootPath turns a path through the Sysnative alias (a WOW64 process's name for the native system directory,
// docs/installer.md §2) into the same path through System32, for an operation that Windows carries out at the next
// restart. The list of those operations is processed at boot, where Sysnative does not exist: VM run of the real-user
// bundle ti4-w10x64 (tool 7c4165b) stored \??\C:\Windows\Sysnative\<file> as given, and the file was not deleted;
// the same file marked as System32\<file> with the thread's file-system redirection switched off was stored as
// \??\C:\Windows\System32\<file> and deleted. windir is the Windows directory (GetSystemWindowsDirectoryW). ok
// reports whether p went through Sysnative; then the caller switches redirection off for the call, or Windows
// would redirect the System32 path to SysWOW64.
func BootPath(p, windir string) (string, bool) {
	prefix := strings.TrimRight(windir, `\`) + `\Sysnative\`
	if len(p) > len(prefix) && strings.EqualFold(p[:len(prefix)], prefix) {
		return strings.TrimRight(windir, `\`) + `\System32\` + p[len(prefix):], true
	}
	return p, false
}
