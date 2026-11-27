package toolsetup

import (
	"fmt"
	"strings"
)

// The wizard's model (docs/installer-cli.md §12.5): which boxes it shows, ticked and locked, from what is installed
// and what this installer carries, and the request its choices make. The Windows window (installer/toolstub) only
// draws this model; every rule is here and tested on any OS. The wizard sets nothing but the properties of
// Request: with its boxes as they start, its request gives the same plan as the command line without properties.

// Box is one check box.
type Box struct {
	Label   string
	Visible bool
	Checked bool
	Enabled bool
	// Note is shown under the box (why it is greyed or locked, what it does).
	Note string
}

// Wizard is the state of one wizard window.
type Wizard struct {
	Uninstall bool
	// Change: the install wizard on a machine where YAKBD is installed (Settings' Modify).
	Change                                  bool
	Editor, CLI, Path, Settings, PathRemove Box

	s           *Setup
	st          *State
	build       string
	hasUserData bool
}

// Box ids, the order of the wizard's controls.
const (
	BoxEditor     = "editor"
	BoxCLI        = "cli"
	BoxPath       = "path"
	BoxSettings   = "settings"
	BoxPathRemove = "pathremove"
)

// NewWizard makes the wizard for a machine: the install (or change) wizard, or the uninstall wizard. hasUserData
// tells whether the starting user has the editor's settings (HasUserData).
func NewWizard(s *Setup, st *State, build string, uninstall, hasUserData bool) (*Wizard, error) {
	w := &Wizard{Uninstall: uninstall, s: s, st: st, build: build, hasUserData: hasUserData}
	w.Editor.Label, w.CLI.Label = ComponentName[Editor], ComponentName[CLI]
	w.Path.Label = "Add YAKBD CLI to PATH"
	w.Settings.Label = "Remove my settings"
	w.PathRemove.Label = "Remove YAKBD CLI from PATH"
	if uninstall {
		if !st.Has(CLI) && !st.Has(Editor) {
			return nil, fail(1605, "YAKBD is not installed; nothing was changed.")
		}
		w.CLI = Box{Label: w.CLI.Label, Visible: st.Has(CLI), Checked: st.Has(CLI)}
		w.Editor = Box{Label: w.Editor.Label, Visible: st.Has(Editor), Checked: st.Has(Editor)}
		w.relock()
		return w, nil
	}
	w.Change = st.Has(CLI) || st.Has(Editor)
	def, err := s.Decide(st, build, Request{})
	if err != nil {
		return nil, err
	}
	editorCarried := s.P.Manifest.Component(Editor) != nil
	w.Editor.Visible = editorCarried || st.Has(Editor)
	w.Editor.Checked = def.Final(Editor)
	w.CLI.Visible, w.CLI.Checked = true, def.Final(CLI)
	w.Path.Checked = def.Path
	switch {
	case editorCarried && !s.P.Supports(Editor, build) && build == BuildX86:
		w.Editor.Note = "The editor needs 64-bit Windows."
	case editorCarried && !s.P.Supports(Editor, build):
		w.Editor.Note = "This installer has no editor for this computer."
	case !editorCarried && st.Has(Editor):
		w.Editor.Note = "Installed; this installer does not carry the editor (untick to remove it)."
	}
	w.relock()
	return w, nil
}

// relock applies the locks after a change: in the install wizard the CLI box is locked on while the editor box is
// ticked (the editor needs the CLI), and the editor box can only be ticked when this installer carries the editor
// for the machine; in the uninstall wizard the editor box is locked on while the CLI box is ticked (removing the CLI
// removes the editor). The PATH boxes follow.
func (w *Wizard) relock() {
	if w.Uninstall {
		w.CLI.Enabled = w.CLI.Visible
		w.Editor.Enabled = w.Editor.Visible && !w.CLI.Checked
		if w.CLI.Checked && w.Editor.Visible {
			w.Editor.Checked, w.Editor.Note = true, "Removed with YAKBD CLI, which it needs."
		} else {
			w.Editor.Note = ""
		}
		removingEditor := w.Editor.Visible && w.Editor.Checked
		w.Settings.Visible = removingEditor && w.hasUserData
		if !w.Settings.Visible {
			w.Settings.Checked = false
		}
		w.Settings.Enabled = w.Settings.Visible
		w.PathRemove.Visible = w.CLI.Visible && !w.CLI.Checked && w.st.PathPresent
		w.PathRemove.Enabled = w.PathRemove.Visible
		if !w.PathRemove.Visible {
			w.PathRemove.Checked = false
		}
		if w.CLI.Checked && w.st.PathPresent {
			w.CLI.Note = "Its PATH entry is removed with it."
		} else {
			w.CLI.Note = ""
		}
		return
	}
	carried := w.s.P.Supports(Editor, w.build)
	w.Editor.Enabled = w.Editor.Visible && (carried || w.Editor.Checked && w.st.Has(Editor))
	if w.Editor.Checked {
		w.CLI.Checked, w.CLI.Enabled = true, false
		w.CLI.Note = "Needed by YAKBD Editor."
	} else {
		w.CLI.Enabled, w.CLI.Note = true, ""
	}
	w.Path.Visible, w.Path.Enabled = w.CLI.Checked, w.CLI.Checked
}

// Toggle flips a box as a click does, when it is enabled.
func (w *Wizard) Toggle(id string) {
	b := w.box(id)
	if b == nil || !b.Visible || !b.Enabled {
		return
	}
	b.Checked = !b.Checked
	w.relock()
}

func (w *Wizard) box(id string) *Box {
	return map[string]*Box{BoxEditor: &w.Editor, BoxCLI: &w.CLI, BoxPath: &w.Path, BoxSettings: &w.Settings, BoxPathRemove: &w.PathRemove}[id]
}

// Request is what the boxes ask for, as the command line's properties.
func (w *Wizard) Request() Request {
	if w.Uninstall {
		if w.CLI.Checked || !w.CLI.Visible && w.Editor.Checked {
			return Request{Uninstall: true, RemoveUserData: w.Settings.Checked}
		}
		req := Request{RemoveUserData: w.Settings.Checked, AddLocal: []string{}}
		if w.Editor.Checked {
			req.Remove = []string{Editor}
		}
		if w.PathRemove.Visible {
			on := !w.PathRemove.Checked
			req.AddToPath = &on
		}
		return req
	}
	req := Request{AddLocal: []string{}}
	for _, b := range []struct {
		id  string
		box *Box
	}{{CLI, &w.CLI}, {Editor, &w.Editor}} {
		switch {
		case b.box.Checked && (w.s.P.Supports(b.id, w.build)):
			req.AddLocal = append(req.AddLocal, b.id)
		case !b.box.Checked && w.st.Has(b.id):
			req.Remove = append(req.Remove, b.id)
		}
	}
	if w.CLI.Checked {
		on := w.Path.Checked
		req.AddToPath = &on
	}
	return req
}

// CanGo reports whether the wizard's main button (Install, Change, Remove) does anything.
func (w *Wizard) CanGo() bool {
	if w.Uninstall {
		return w.CLI.Checked || w.Editor.Checked
	}
	return w.CLI.Checked || w.Change
}

// Plan is the plan of the boxes as they are.
func (w *Wizard) Plan() (*Plan, error) { return w.s.Decide(w.st, w.build, w.Request()) }

// Installed lists what is installed, for the wizard's first lines ("YAKBD CLI 0.1.0-preview.2 is installed").
func (w *Wizard) Installed() []string {
	var out []string
	for _, id := range ComponentIDs {
		if w.st.Has(id) {
			where := ""
			if w.st.Legacy != nil {
				where = " (earlier layout)"
			}
			out = append(out, upper(fmt.Sprintf("%s is installed%s.", w.st.NameOf(id), where)))
		}
	}
	if len(out) == 0 {
		out = append(out, "YAKBD is not installed on this computer.")
	}
	return out
}

// upper starts a sentence with a capital letter.
func upper(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Summary says what the plan will do, one line per component and for the PATH, then the plan's notes.
func (w *Wizard) Summary() []string {
	p, err := w.Plan()
	if err != nil {
		return []string{err.Error()}
	}
	var out []string
	for _, id := range ComponentIDs {
		old, mine := w.st.VersionOf(id), ""
		if c := w.s.P.Manifest.Component(id); c != nil {
			mine = c.Version
		}
		name, was := ComponentName[id], upper(w.st.NameOf(id))
		switch p.Comp[id] {
		case Install:
			if w.st.Legacy != nil && w.st.Has(id) {
				out = append(out, fmt.Sprintf("%s %s will be installed in place of %s.", name, mine, w.st.NameOf(id)))
			} else {
				out = append(out, fmt.Sprintf("%s %s will be installed.", name, mine))
			}
		case Upgrade:
			out = append(out, fmt.Sprintf("%s will be upgraded from %s to %s.", name, old, mine))
		case Repair:
			out = append(out, fmt.Sprintf("%s %s will be repaired (written again).", name, mine))
		case Keep:
			out = append(out, fmt.Sprintf("%s stays as it is.", was))
		case KeepNewer:
			out = append(out, fmt.Sprintf("%s stays: it is newer than this installer's %s.", was, mine))
		case Remove:
			out = append(out, fmt.Sprintf("%s will be removed.", was))
		}
	}
	switch {
	case p.Path && !w.st.PathPresent:
		out = append(out, "YAKBD CLI will be added to PATH.")
	case !p.Path && w.st.PathPresent:
		out = append(out, "YAKBD CLI will be removed from PATH.")
	}
	if p.RemoveUserData {
		out = append(out, "Your YAKBD Editor settings will be removed.")
	}
	for _, n := range p.Notes {
		if !strings.Contains(n, "newer than this installer's") { // said above
			out = append(out, n)
		}
	}
	return out
}
