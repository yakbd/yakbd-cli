// Package postable answers whether a position is a key of some target: whether any position table of the kbl
// specification (SPEC §16: windows, macos, xkb) has a row for it. A bound position that some table has and the
// building target's table has not is a key that target does not have (macos.positionWithoutKey,
// xkb.positionWithoutKey, D145); a position no table has needs a pin.
package postable

import (
	"github.com/yakbd/yakbd-cli/target/macos/keycode"
	"github.com/yakbd/yakbd-cli/target/windows/vk"
	"github.com/yakbd/yakbd-cli/target/xkb/keyname"
)

// Known reports whether some target's position table has a row for the position.
func Known(p string) bool {
	if _, ok := vk.DefaultForPosition(p); ok {
		return true
	}
	if _, ok := keycode.ForPosition(p); ok {
		return true
	}
	_, ok := keyname.ForPosition(p)
	return ok
}
