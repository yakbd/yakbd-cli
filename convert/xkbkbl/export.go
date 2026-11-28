package xkbkbl

import (
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/xkb/build"
)

// ToXKB is the XKB build of a layout (targets/xkb.md): the symbols file and, when the layout has dead keys, the
// Compose file (nil otherwise).
func ToXKB(l *model.Layout, opt build.Options) (symbols, compose []byte, r *loss.Report) {
	res, r := build.Build(l, opt)
	if res == nil {
		return nil, nil, r
	}
	return res.SymbolsText, res.ComposeText, r
}
