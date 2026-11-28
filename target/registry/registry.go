// Package registry lists the targets the command line knows (docs/targets.md). A new platform adds its package
// under target/ and one line here.
package registry

import (
	"github.com/yakbd/yakbd-cli/target"
	"github.com/yakbd/yakbd-cli/target/windows/wintarget"
)

// Default is the target of a command line without -target.
const Default = "windows"

// Targets are the known targets.
var Targets = []target.Target{
	wintarget.Target{},
}

// Lookup returns the target called name.
func Lookup(name string) (target.Target, bool) { return target.Lookup(Targets, name) }

// Names returns the known targets' names, sorted.
func Names() []string { return target.Names(Targets) }
