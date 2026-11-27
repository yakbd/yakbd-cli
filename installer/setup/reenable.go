package setup

import (
	"fmt"
	"strings"
	"time"
)

// addToList calls InstallLayoutOrTip(str, 0) for the user this process runs as until User Profile lists the KLID.
// TRUE proves nothing (inst section 4.3), and of several quick calls only the first may reach the list (yakbd
// sign-in run): read User Profile back and call again while the value is
// missing.
func (s *Setup) addToList(sid, klid, str string) error {
	in := false
	for try := 1; try <= 3 && !in; try++ {
		if try > 1 && s.Sleep != nil {
			s.Sleep(time.Duration(try) * 500 * time.Millisecond)
		}
		if err := s.Sys.InstallLayoutOrTip(str, 0); err != nil {
			return fail(ExitEnable, "The layout is installed, but adding it to your input list failed (InstallLayoutOrTip(%q): %v). Add it in Settings > Time & language > Language & region.", str, err)
		}
		in = s.inProfile(sid, klid)
		s.logf("enable: InstallLayoutOrTip(%q, 0) for user %s, try %d: in User Profile %v", str, sid, try, in)
	}
	if !in {
		return fail(ExitEnable, "The layout is installed, but Windows did not add it to your input list. Add it in Settings > Time & language > Language & region.")
	}
	return nil
}

// loaded reports whether GetKeyboardLayoutList of this process has the HKL, and logs the list.
func (s *Setup) loaded(hkl uint32) bool {
	list, err := s.Sys.LayoutList()
	var hs []string
	found := false
	for _, h := range list {
		hs = append(hs, fmt.Sprintf("%08x", h))
		found = found || h == hkl
	}
	s.logf("GetKeyboardLayoutList: %s (%v); HKL %08x there: %v", strings.Join(hs, " "), errText(err), hkl, found)
	return err == nil && found
}

// Before is what Reenable needs from before an upgrade or repair: the recorded KLID and Layout Id, and whether
// the user this process runs as had the layout in its list.
type Before struct {
	KLID, LayoutID string
	Enabled        bool
}

// Before reads the state Reenable compares with. It needs no elevation.
func (s *Setup) Before() Before {
	rec, ok := s.readRecord()
	if !ok {
		return Before{}
	}
	sid, err := s.Sys.UserSID()
	if err != nil {
		return Before{}
	}
	return Before{KLID: rec.klid, LayoutID: rec.layoutID, Enabled: s.refsIn(sid, map[string]bool{strings.ToLower(rec.klid): true}).refs}
}

// Reenable enables the layout again for the user this process runs as when an upgrade or repair had to give it a
// new Layout Id and the user had it before (kbl targets/windows-install.md §5 "re-enables the layout where it was
// enabled"; inst §10 UPGRADE_IN_PLACE). The KLID is the same, so the user's entries (Preload, Substitutes, User
// Profile) still name the layout; what changes is the HKL, MAKELONG(LANGID, 0xF000 | Layout Id) (inst §3.2), which
// the CTF state holds (inst §4.5, OBS I19). InstallLayoutOrTip rewrites that state (inst §4.3, from the Keyman wiki).
// For each "<LANGID>:<KLID>" string the user's User Profile has (a 0C00 layout is listed under its transient
// LANGID) the layout is removed (ILOT_UNINSTALL) and added again (VM-9: a second plain call leaves the old HKL in the
// session), not removed when it is the user's only layout; the add reads User Profile back. The new HKL is looked
// for in GetKeyboardLayoutList, logged only (this process's list may lag behind, yakbd VM run 2). It does nothing
// when the Layout Id is unchanged or the user did not have the layout; ran reports whether it did something. Other
// users' hives are not changed (D69).
func (s *Setup) Reenable(b Before) (res *Result, ran bool, err error) {
	rec, ok := s.readRecord()
	if !ok || !b.Enabled || b.LayoutID == "" || !strings.EqualFold(rec.klid, b.KLID) || strings.EqualFold(rec.layoutID, b.LayoutID) {
		return nil, false, nil
	}
	sid, _ := s.Sys.UserSID()
	set := map[string]bool{strings.ToLower(rec.klid): true}
	h := s.refsIn(sid, set)
	strs := map[string]bool{}
	for _, pv := range h.profileVals {
		strs[strings.ToUpper(pv[strings.IndexByte(pv, '\\')+1:])] = true
	}
	if len(strs) == 0 {
		lang := strings.ToUpper(rec.langID)
		if lang == "0C00" {
			return nil, true, fail(ExitEnable, "The layout has a new Layout Id, but its language is not in your list any more: add the layout in Settings > Time & language > Language & region.")
		}
		strs[lang+":"+strings.ToUpper(rec.klid)] = true
	}
	s.logf("re-enable: Layout Id %s -> %s for user %s, strings %v", b.LayoutID, rec.layoutID, sid, sortedKeys(strs))
	res = &Result{KLID: rec.klid, LayoutID: rec.layoutID}
	all := func() bool {
		ok := true
		for _, str := range sortedKeys(strs) {
			ok = s.loaded(HKL(hex16(str[:strings.IndexByte(str, ':')]), hex16(rec.layoutID))) && ok
		}
		return ok
	}
	// One call for a layout already in the list does not load the new HKL: the session keeps the old one (VM-9, yakbd
	// VM run 3). So the layout is removed and added again (D69 "next"), unless it is the user's only layout.
	only := h.onlyLayout(set)
	for _, str := range sortedKeys(strs) {
		if only {
			s.logf("re-enable: the layout is this user's only layout: not removed first")
		} else {
			err := s.Sys.InstallLayoutOrTip(str, ILOTUninstall)
			s.logf("re-enable: InstallLayoutOrTip(%q, ILOT_UNINSTALL): %v", str, errText(err))
		}
		if err := s.addToList(sid, rec.klid, str); err != nil {
			return nil, true, err
		}
	}
	if all() {
		res.note("The layout has a new Layout Id; it was enabled for you again.")
		return res, true, nil
	}
	res.note("The layout has a new Layout Id and is in your input list; if it does not appear, sign out and sign in again.")
	return res, true, nil
}
