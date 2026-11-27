package build

import (
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
)

// Pin returns a copy of l whose targets.windows holds every value the default rules decided, and whose
// positions include every Table A position the DLL contains (targets/windows.md §10). Building the pinned file
// gives the same DLL and no windowsDefault.* entry. Other general data is not changed.
func Pin(l *model.Layout, opt Options) (*model.Layout, *loss.Report) {
	res, r := Lower(l, opt)
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
	w := *res.Pins
	if l.Targets != nil && l.Targets.Windows != nil {
		w.OnLoss = l.Targets.Windows.OnLoss
	}
	t.Windows = &w
	out.Targets = &t
	return &out, r
}
