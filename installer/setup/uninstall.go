package setup

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Uninstall removes the product: every KLID it owns from every loaded user hive, the KLID keys, the files, the
// uninstall entry and the installed copy of the installer (inst §10 UNINSTALL and its refusal policy;
// docs/installer.md §2.4). force overrides the refusal for a hive whose only layout this is.
func (s *Setup) Uninstall(force bool) (*Result, error) {
	if err := s.requireAdmin(); err != nil {
		return nil, err
	}
	m := s.m()
	ks, err := s.scanKLIDs()
	if err != nil {
		return nil, err
	}
	rec, hasRec := s.readRecord()
	set := map[string]bool{}
	files := map[string]bool{}
	var klids []string
	for _, k := range ks {
		if strings.EqualFold(k.product, m.Product.Code) {
			set[strings.ToLower(k.name)] = true
			klids = append(klids, k.name)
			if k.file != "" {
				files[strings.ToLower(k.file)] = true
			}
		}
	}
	if hasRec {
		files[strings.ToLower(rec.file)] = true
		if !set[strings.ToLower(rec.klid)] {
			set[strings.ToLower(rec.klid)] = true
			// The key may be gone (M7); it is still removed from the hives.
		}
	}
	if len(set) == 0 && !hasRec {
		s.logf("uninstall: product %s is not installed", m.Product.Code)
		return nil, fail(ExitNotInstalled, "The layout is not installed; nothing was changed.")
	}
	res := &Result{}
	if hasRec {
		res.KLID, res.LayoutID = rec.klid, rec.layoutID
	} else if len(klids) > 0 {
		res.KLID = klids[0]
	}
	s.logf("uninstall %s: KLIDs %v", m.Product.Code, keys(set))

	// 1. Per-user state: refuse first, then remove.
	sid, _ := s.Sys.UserSID()
	var refs []hiveRefs
	var only []string
	for _, h := range s.hives() {
		if systemHive(h) {
			continue // not touched (kbl targets/windows-install.md section 9, D57)
		}
		r := s.refsIn(h, set)
		if !r.refs {
			continue
		}
		refs = append(refs, r)
		if r.onlyLayout(set) {
			only = append(only, h)
		}
	}
	if len(only) > 0 && !force {
		return nil, fail(ExitRefused, "The layout is the only keyboard layout of %s. Add another layout there first (or run the uninstaller with FORCE=1). Nothing was changed.", strings.Join(only, ", "))
	}
	// The §7.1 places first, always (kbl targets/windows-install.md §9 step 1); a refusal there changes nothing.
	if err := s.cleanSignIn(set, force, res); err != nil {
		return nil, err
	}
	lang := strings.ToUpper(m.Layout.LangID)
	for _, r := range refs {
		s.removeFromHive(r, set, sid, lang)
	}

	// 2. KLID keys.
	for _, k := range klids {
		if err := s.Sys.DeleteKey(klidKey(k)); err != nil {
			return nil, fmt.Errorf("cannot delete KLID %s: %v", k, err)
		}
		s.logf("KLID %s deleted", k)
	}

	// 3. Files.
	native, _ := s.Sys.NativeMachine()
	dirs := []DirKind{DirNative}
	if native != 0x014C {
		dirs = append(dirs, DirWOW64)
	}
	for _, f := range sortedKeys(files) {
		if f == "" {
			continue
		}
		for _, d := range dirs {
			dir, err := s.Sys.Dir(d)
			if err != nil {
				return nil, fmt.Errorf("cannot find %s: %v", d, err)
			}
			// A replacement an upgrade left for the next restart goes first, or it would bring the file back.
			for _, ext := range []string{".yakbd-new", ".yakbd-tmp"} {
				if err := s.Sys.Remove(joinPath(dir, f) + ext); err == nil {
					s.logf("%s deleted", joinPath(dir, f)+ext)
				}
			}
			p := joinPath(dir, f)
			if _, err := s.Sys.ReadFile(p); errors.Is(err, ErrNotFound) {
				continue
			}
			if err := s.Sys.Remove(p); err != nil {
				if !errors.Is(err, ErrInUse) {
					return nil, fmt.Errorf("cannot delete %s: %v", p, err)
				}
				if err := s.Sys.DeleteAtReboot(p); err != nil {
					return nil, fmt.Errorf("cannot schedule the deletion of %s: %v", p, err)
				}
				res.Reboot = true
				s.logf("%s is in use: deleted at the next restart", p)
				continue
			}
			s.logf("%s deleted", p)
		}
	}

	// 4. Language tags the installer added (kbl targets/windows-install.md section 9).
	s.removeAddedTags(sid)

	// 4. Uninstall entry and the installed copy of the installer.
	if err := s.Sys.DeleteKey(s.arpKey()); err != nil {
		return nil, fmt.Errorf("cannot delete the uninstall entry: %v", err)
	}
	s.logf("uninstall entry deleted")
	if dir, err := s.installDir(); err == nil {
		s.removeInstallDir(dir)
	}
	if res.Reboot {
		res.note("A layout file is still in use; restart Windows to finish removing it.")
	}
	return res, nil
}

// removeFromHive takes the layout out of one user hive (inst §10, §4.3).
func (s *Setup) removeFromHive(r hiveRefs, set map[string]bool, sid, lang string) {
	strs := map[string]bool{}
	for _, pv := range r.profileVals {
		strs[strings.ToUpper(pv[strings.IndexByte(pv, '\\')+1:])] = true
	}
	for k := range set {
		strs[lang+":"+strings.ToUpper(k)] = true
	}
	own := strings.EqualFold(r.hive, sid)
	for _, str := range sortedKeys(strs) {
		var err error
		if own {
			err = s.Sys.InstallLayoutOrTip(str, ILOTUninstall)
			s.logf("hive %s (this user): InstallLayoutOrTip(%q, ILOT_UNINSTALL): %v", r.hive, str, errText(err))
		} else {
			err = s.Sys.InstallLayoutOrTipUserReg(r.hive, str, ILOTUninstall)
			s.logf("hive %s: InstallLayoutOrTipUserReg(%q, ILOT_UNINSTALL): %v", r.hive, str, errText(err))
		}
	}
	base := Users + `\` + r.hive
	if !own {
		// InstallLayoutOrTipUserReg leaves User Profile alone (inst §4.3).
		for _, pv := range r.profileVals {
			i := strings.IndexByte(pv, '\\')
			p := base + `\Control Panel\International\User Profile\` + pv[:i]
			if err := s.Sys.DeleteValue(p, pv[i+1:]); err == nil {
				s.logf("hive %s: User Profile value %s removed", r.hive, pv)
			}
		}
	}
	// What is still left is removed by hand (inst §10 fallback).
	left := s.refsIn(r.hive, set)
	if !left.refs {
		if !own {
			s.renumberPreload(r.hive, left.preload)
		}
		return
	}
	if own {
		s.logf("hive %s (this user): references left after the API call; removing them by hand", r.hive)
	}
	drop := map[string]bool{}
	for name, d := range left.subst {
		if set[d] {
			if err := s.Sys.DeleteValue(base+`\Keyboard Layout\Substitutes`, name); err == nil {
				s.logf("hive %s: Substitutes %s removed", r.hive, name)
			}
			drop[name] = true
		}
	}
	for k := range set {
		drop[k] = true
	}
	var keep []string
	changed := false
	for _, v := range left.preload {
		if drop[strings.ToLower(v.Str)] {
			changed = true
			continue
		}
		keep = append(keep, v.Str)
	}
	if changed {
		pk := base + `\Keyboard Layout\Preload`
		for _, v := range left.preload {
			_ = s.Sys.DeleteValue(pk, v.Name)
		}
		for i, k := range keep {
			_ = s.Sys.SetString(pk, fmt.Sprint(i+1), k, false)
		}
		s.logf("hive %s: Preload renumbered (%d left)", r.hive, len(keep))
	}
	for _, pv := range left.profileVals {
		i := strings.IndexByte(pv, '\\')
		_ = s.Sys.DeleteValue(base+`\Control Panel\International\User Profile\`+pv[:i], pv[i+1:])
		s.logf("hive %s: User Profile value %s removed", r.hive, pv)
	}
}

// removeInstallDir deletes the installed copy of the installer. The running program cannot delete itself: it
// moves itself into the temporary folder and is deleted there at the next restart.
func (s *Setup) removeInstallDir(dir string) {
	exe := joinPath(dir, "setup.exe")
	self := s.Sys.SelfPath()
	if samePath(self, exe) {
		tmp := joinPath(s.Sys.TempDir(), "yakbd-uninstall-"+strings.Trim(s.m().Product.Code, "{}")+".exe")
		_ = s.Sys.Remove(tmp)
		if err := s.Sys.Rename(exe, tmp); err == nil {
			_ = s.Sys.DeleteAtReboot(tmp)
			s.logf("the running uninstaller moved to %s (deleted at the next restart)", tmp)
		} else {
			_ = s.Sys.DeleteAtReboot(exe)
			_ = s.Sys.DeleteAtReboot(dir)
			s.logf("%s cannot be moved (%v): deleted at the next restart", exe, err)
			return
		}
	} else if err := s.Sys.Remove(exe); err != nil && !errors.Is(err, ErrNotFound) {
		s.logf("cannot delete %s: %v", exe, err)
	}
	if err := s.Sys.RemoveDir(dir); err == nil {
		s.logf("%s deleted", dir)
	}
	pf, _ := s.Sys.ProgramFiles()
	_ = s.Sys.RemoveDir(joinPath(pf, "YAKBD")) // only when empty
}

func errText(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

func keys(m map[string]bool) []string { return sortedKeys(m) }

func sortedKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// renumberPreload rewrites a hive's Preload as 1..n when an entry was taken out of the middle.
func (s *Setup) renumberPreload(hive string, pre []Value) {
	gap := false
	for i, v := range pre {
		if v.Name != fmt.Sprint(i+1) {
			gap = true
		}
	}
	if !gap {
		return
	}
	pk := Users + `\` + hive + `\Keyboard Layout\Preload`
	for _, v := range pre {
		_ = s.Sys.DeleteValue(pk, v.Name)
	}
	for i, v := range pre {
		_ = s.Sys.SetString(pk, fmt.Sprint(i+1), v.Str, false)
	}
	s.logf("hive %s: Preload renumbered (%d left)", hive, len(pre))
}

// systemHive reports whether a hive under HKU is the welcome screen's or a service account's: .DEFAULT, which is
// S-1-5-18 (inst §4.7), and S-1-5-19, S-1-5-20.
func systemHive(h string) bool {
	switch strings.ToUpper(h) {
	case ".DEFAULT", "S-1-5-18", "S-1-5-19", "S-1-5-20":
		return true
	}
	return ownHive(h)
}
