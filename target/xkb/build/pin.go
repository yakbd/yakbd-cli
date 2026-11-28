package build

import (
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
)

// Pin returns a copy of l with every value of targets.xkb a build decides written into the file, so that a
// later build uses no default rule.
func Pin(l *model.Layout, opt Options) (*model.Layout, *loss.Report) {
	res, r := Build(l, opt)
	if res == nil {
		return nil, r
	}
	out := *l
	t := model.Targets{}
	if l.Targets != nil {
		t = *l.Targets
	}
	x := *res.Pins
	if l.Targets != nil && l.Targets.XKB != nil {
		x.OnLoss = l.Targets.XKB.OnLoss
	}
	t.XKB = &x
	out.Targets = &t
	return &out, r
}
