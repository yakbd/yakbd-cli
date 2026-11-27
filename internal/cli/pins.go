package cli

import (
	"strings"

	"github.com/yakbd/yakbd-cli/model"
)

// setLangID pins registration.langId.
func setLangID(l *model.Layout, id string) {
	if l.Targets == nil {
		l.Targets = &model.Targets{}
	}
	if l.Targets.Windows == nil {
		l.Targets.Windows = &model.WindowsTarget{}
	}
	if l.Targets.Windows.Registration == nil {
		l.Targets.Windows.Registration = &model.Registration{}
	}
	l.Targets.Windows.Registration.LangID = strings.ToUpper(id)
}

// extraWith adds the kinds of -accept to the command line's policy.
func (rf *reportFlags) extraWith(accept string) *model.OnLoss {
	if accept == "" {
		return rf.extra()
	}
	p := &model.OnLoss{Kinds: map[string]string{}}
	if base := rf.extra(); base != nil {
		for k, v := range base.Kinds {
			p.Kinds[k] = v
		}
	}
	for _, k := range strings.Split(accept, ",") {
		p.Kinds[strings.TrimSpace(k)] = "accept"
	}
	return p
}
