package keylayoutkbl

import (
	"github.com/yakbd/yakbd-cli/format/keylayout"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/macos/build"
)

// ToKeylayout writes a layout as a .keylayout file: the macOS build (target/macos/build, targets/macos.md), its
// losses and defaulted choices resolved under the file's policies and opt.Policy. Nil data when the build fails.
func ToKeylayout(l *model.Layout, opt build.Options) ([]byte, *loss.Report) {
	res, r := build.Build(l, opt)
	if res == nil {
		return nil, r
	}
	return keylayout.Write(res.Keyboard), r
}
