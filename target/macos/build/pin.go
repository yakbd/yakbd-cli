package build

import (
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
)

// Pin returns a copy of l whose targets.macos holds every value the default rules decided, and whose positions
// include every table position the layout gets through standardKeys (targets/macos.md §6). Building the pinned
// file gives the same layout and no macosDefault.* entry. Other general data is not changed.
func Pin(l *model.Layout, opt Options) (*model.Layout, *loss.Report) {
	res, r := Build(l, opt)
	if res == nil {
		return nil, r
	}
	out := *l
	out.Positions = map[string]*model.Position{}
	for k, v := range l.Positions {
		out.Positions[k] = v
	}
	for k, v := range res.AddedPositions {
		out.Positions[k] = v
	}
	t := model.Targets{}
	if l.Targets != nil {
		t = *l.Targets
	}
	m := *res.Pins
	if l.Targets != nil && l.Targets.MacOS != nil {
		m.OnLoss = l.Targets.MacOS.OnLoss
	}
	t.MacOS = &m
	out.Targets = &t
	return &out, r
}
