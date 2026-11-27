package setup

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/yakbd/yakbd-cli/installer/payload"
)

// The sign-in screen and new users (kbl targets/windows-install.md §7.1 and §9 step 1, D87). The installer writes,
// for its own KLID only, the entries inst §4.7 T8 measured for Windows' own copy into "the §7.1 places": HKU\.DEFAULT
// (the sign-in screen), HKU\S-1-5-19, HKU\S-1-5-20 and the Default profile (the hive new accounts are made from),
// which is loaded for the run under DefaultHiveName and unloaded again on every path.

// RecSignIn is the record value, in the uninstall entry, that says whether §7.1 was applied: "1" or "0".
const RecSignIn = "YakbdSignIn"

// systemPlaces are the §7.1 places that are loaded hives of their own (the Default profile comes on top).
var systemPlaces = []string{".DEFAULT", "S-1-5-19", "S-1-5-20"}

// DefaultHiveName is the name under HKEY_USERS the Default profile is loaded as: "YAKBD-<product code>", a name no
// other key there has (§7.1).
func (s *Setup) DefaultHiveName() string { return "YAKBD-" + s.m().Product.Code }

// ownHive reports whether a hive name under HKU is one this program loads (the Default profile).
func ownHive(h string) bool { return strings.HasPrefix(strings.ToUpper(h), "YAKBD-") }

// place is one §7.1 place for a run: its hive name and whether it gets the System Backup value.
type place struct {
	hive   string
	backup bool
}

// systemLoaded returns the system places that are loaded (.DEFAULT always is; S-1-5-19 and S-1-5-20 are skipped and
// logged when they are not).
func (s *Setup) systemLoaded() []place {
	names, _ := s.Sys.SubKeys(Users)
	have := map[string]bool{}
	for _, n := range names {
		have[strings.ToUpper(n)] = true
	}
	var out []place
	for _, h := range systemPlaces {
		if !have[strings.ToUpper(h)] {
			s.logf("sign-in screen: HKU\\%s is not loaded: skipped", h)
			continue
		}
		out = append(out, place{hive: h, backup: true})
	}
	return out
}

// withDefault loads the Default profile, runs f with its hive name and unloads it again, also when f fails. loadErr
// is the failure to load (f did not run); err is f's error. An unload that fails is not an error of the run (the
// hive stays loaded until the restart, §7.1): it is logged and added to res's notes. A hive of that name that is
// still loaded (an earlier run whose unload failed) is used as it is and unloaded at the end.
func (s *Setup) withDefault(res *Result, f func(hive string) error) (loadErr, err error) {
	name := s.DefaultHiveName()
	if s.hiveLoaded(name) {
		s.logf("Default profile: HKU\\%s is still loaded (an earlier run could not unload it): used as it is", name)
	} else {
		file, e := s.Sys.DefaultProfileHive()
		if e != nil {
			return fmt.Errorf("cannot find the Default profile: %v", e), nil
		}
		if e := s.Sys.LoadHive(name, file); e != nil {
			s.logf("Default profile %s: cannot load it as HKU\\%s: %v", file, name, e)
			return fmt.Errorf("cannot load the Default profile %s: %v", file, e), nil
		}
		s.logf("Default profile %s loaded as HKU\\%s", file, name)
	}
	err = f(name)
	if e := s.Sys.UnloadHive(name); e != nil {
		s.logf("Default profile: unloading HKU\\%s failed: %v (it stays loaded until the restart)", name, e)
		res.note("The Default profile could not be closed (HKU\\%s: %v); Windows closes it at the next restart.", name, e)
	} else {
		s.logf("Default profile unloaded (HKU\\%s)", name)
	}
	return nil, err
}

// hiveLoaded reports whether HKU has a key of that name.
func (s *Setup) hiveLoaded(name string) bool {
	names, _ := s.Sys.SubKeys(Users)
	for _, n := range names {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// layoutString is "<LLLL>:<KLID>" in upper case (§7).
func layoutString(lang, klid string) string {
	return strings.ToUpper(lang) + ":" + strings.ToUpper(klid)
}

// preloadIndex is the number of the Preload entry that resolves (through Substitutes) to the KLID, 0 when none.
func (h hiveRefs) preloadIndex(klid string) int {
	for _, v := range h.preload {
		k := strings.ToLower(v.Str)
		if d, ok := h.subst[k]; ok {
			k = d
		}
		if k == strings.ToLower(klid) {
			n, _ := strconv.Atoi(v.Name)
			return n
		}
	}
	return 0
}

// languageTag is the subkey of a User Profile key that holds a value of the LANGID.
func (s *Setup) languageTag(key, lang string) string {
	tags, _ := s.Sys.SubKeys(key)
	for _, t := range tags {
		for _, v := range s.values(key + `\` + t) {
			if strings.HasPrefix(strings.ToUpper(v.Name), strings.ToUpper(lang)+":") {
				return t
			}
		}
	}
	return ""
}

func hasValue(vs map[string]Value, name string) bool {
	_, ok := vs[strings.ToLower(name)]
	return ok
}

// errNoLanguage: a place lacks the layout's language and no loaded user hive gives its tag (§7.1, D97, Q14).
var errNoLanguage = errors.New("no user who has the language")

// userTag is the tag under which Windows filed the LANGID in a loaded user hive (HKU\S-1-5-21-*, this process's
// user first): a User Profile subkey holding a "<LLLL>:" value (§7.1, D97). "" when none.
func (s *Setup) userTag(lang string) string {
	sid, _ := s.Sys.UserSID()
	hives := []string{sid}
	for _, h := range s.hives() {
		if strings.HasPrefix(strings.ToUpper(h), "S-1-5-21-") && !strings.EqualFold(h, sid) {
			hives = append(hives, h)
		}
	}
	for _, h := range hives {
		if h == "" {
			continue
		}
		if t := s.languageTag(userProfile(h), lang); t != "" {
			s.logf("sign-in screen: language %s is filed as %s in hive %s", strings.ToUpper(lang), t, h)
			return t
		}
	}
	return ""
}

// placeKey is a place's name in the record of added tags.
func placeKey(hive string) string {
	if ownHive(hive) {
		return "Default"
	}
	return strings.ToUpper(hive)
}

// addTag appends tag to the Languages value of key (read-modify-write, inst §4.4) and creates key\<tag>; u gets the
// way back.
func (s *Setup) addTag(key, tag string, u *undo) error {
	if _, err := s.Sys.CreateKey(key); err != nil {
		return fmt.Errorf("cannot create %s: %v", key, err)
	}
	old := multi(s.values(key)["languages"])
	if !containsFold(old, tag) {
		if err := s.Sys.SetMultiString(key, "Languages", append(append([]string(nil), old...), tag)); err != nil {
			return fmt.Errorf("cannot write %s\\Languages: %v", key, err)
		}
		u.push(func() { s.dropTag(key, tag) })
		s.logf("sign-in screen: %s\\Languages: %s added", key, tag)
	}
	if _, err := s.Sys.CreateKey(key + `\` + tag); err != nil {
		return fmt.Errorf("cannot create %s\\%s: %v", key, tag, err)
	}
	return nil
}

// dropTag removes tag from key's Languages and deletes key\<tag>, when that subkey holds no layout value any more.
func (s *Setup) dropTag(key, tag string) {
	for _, v := range s.values(key + `\` + tag) {
		if strings.Contains(v.Name, ":") {
			s.logf("sign-in screen: %s\\%s still holds %s: the language stays", key, tag, v.Name)
			return
		}
	}
	var keep []string
	for _, l := range multi(s.values(key)["languages"]) {
		if !strings.EqualFold(l, tag) {
			keep = append(keep, l)
		}
	}
	_ = s.Sys.SetMultiString(key, "Languages", keep)
	if err := s.Sys.DeleteKey(key + `\` + tag); err == nil {
		s.logf("sign-in screen: language %s removed from %s", tag, key)
	}
}

// applyPlace writes the missing §7.1 entries into one place, pushing onto u how to take back exactly what it wrote.
// A tag it adds to the place (the language was missing) is appended to added as "<place>=<tag>".
func (s *Setup) applyPlace(p place, klid, lang string, u *undo, added *[]string) error {
	set := map[string]bool{strings.ToLower(klid): true}
	str := layoutString(lang, klid)
	base := Users + `\` + p.hive
	up := base + `\Control Panel\International\User Profile`
	bk := base + `\Control Panel\International\User Profile System Backup`
	// The tag: the subkey that holds the language; else Windows' copy shape with the tag a user hive gives (D97).
	tag := s.languageTag(up, lang)
	if tag == "" {
		if tag = s.userTag(lang); tag == "" {
			tag = s.filedTag
		}
		if tag == "" {
			return errNoLanguage
		}
		s.logf("sign-in screen: hive %s has no language with LANGID %s: adding %s", p.hive, strings.ToUpper(lang), tag)
		if err := s.addTag(up, tag, u); err != nil {
			return err
		}
		if p.backup {
			if err := s.addTag(bk, tag, u); err != nil {
				return err
			}
		}
		*added = append(*added, placeKey(p.hive)+"="+tag)
	}
	// Step 1: Preload and Substitutes (inst §4.3 [VM]: that call writes only these two keys).
	n := s.refsIn(p.hive, set).preloadIndex(klid)
	if n == 0 {
		e := s.Sys.InstallLayoutOrTipUserReg(p.hive, str, 0)
		s.logf("sign-in screen: hive %s: InstallLayoutOrTipUserReg(%q, 0): %v", p.hive, str, errText(e))
		u.push(func() { s.undoPreload(p.hive, set, str) })
		if e != nil {
			return fmt.Errorf("hive %s: InstallLayoutOrTipUserReg(%q) failed: %v", p.hive, str, e)
		}
		if n = s.refsIn(p.hive, set).preloadIndex(klid); n == 0 {
			return fmt.Errorf("hive %s: InstallLayoutOrTipUserReg(%q) wrote no Preload entry for the layout", p.hive, str)
		}
	} else {
		s.logf("sign-in screen: hive %s: Preload %d names the layout already", p.hive, n)
	}
	// Steps 2 and 3: the User Profile value, data = 1 + the highest in its subkey (D97); in the system hives the same
	// value under User Profile System Backup (required, D97).
	keys := []string{up + `\` + tag}
	if p.backup {
		keys = append(keys, bk+`\`+tag)
	}
	num := uint32(0)
	if v, ok := s.values(keys[0])[strings.ToLower(str)]; ok && v.Type == RegDWORD {
		num = v.DWord
	} else {
		for _, v := range s.values(keys[0]) {
			if v.Type == RegDWORD && strings.Contains(v.Name, ":") && v.DWord > num {
				num = v.DWord
			}
		}
		num++
	}
	for _, k := range keys {
		if hasValue(s.values(k), str) {
			continue
		}
		k := k
		created, e := s.Sys.CreateKey(k)
		if e != nil {
			return fmt.Errorf("cannot create %s: %v", k, e)
		}
		u.push(func() {
			_ = s.Sys.DeleteValue(k, str)
			if created {
				s.deleteIfEmpty(k)
				if strings.HasSuffix(strings.ToLower(k), `\user profile system backup\`+strings.ToLower(tag)) {
					s.deleteIfEmpty(k[:len(k)-len(tag)-1])
				}
			}
			s.logf("rollback: %s\\%s removed", k, str)
		})
		if e := s.Sys.SetDWORD(k, str, num); e != nil {
			return fmt.Errorf("cannot write %s\\%s: %v", k, str, e)
		}
		s.logf("sign-in screen: %s\\%s = %d", k, str, num)
	}
	// Read back.
	if s.refsIn(p.hive, set).preloadIndex(klid) != n {
		return fmt.Errorf("hive %s: the Preload entry did not read back", p.hive)
	}
	for _, k := range keys {
		if v, ok := s.values(k)[strings.ToLower(str)]; !ok || v.Type != RegDWORD {
			return fmt.Errorf("%s\\%s did not read back as written", k, str)
		}
	}
	return nil
}

// undoPreload takes back step 1 of one place: the call with ILOT_UNINSTALL, then a Substitutes value still naming the
// KLID and its Preload entry by hand (Preload renumbered).
func (s *Setup) undoPreload(hive string, set map[string]bool, str string) {
	e := s.Sys.InstallLayoutOrTipUserReg(hive, str, ILOTUninstall)
	s.logf("rollback: hive %s: InstallLayoutOrTipUserReg(%q, ILOT_UNINSTALL): %v", hive, str, errText(e))
	r := s.refsIn(hive, set)
	left := false
	for _, d := range r.subst {
		left = left || set[d]
	}
	for _, v := range r.preload {
		left = left || set[strings.ToLower(v.Str)]
	}
	if left {
		r.profileVals = nil // the User Profile values are taken back by their own undo entries (run before this one)
		s.removeFromHive(r, set, "", str[:strings.IndexByte(str, ':')])
	}
}

// placeName is a place's name for the person running the installer.
func placeName(hive string) string {
	switch {
	case strings.EqualFold(hive, ".DEFAULT"):
		return "the sign-in screen (HKU\\.DEFAULT)"
	case ownHive(hive):
		return "the Default profile (new accounts)"
	}
	return "HKU\\" + hive
}

// signInAllowed tells why §7.1 cannot be applied on this machine for this layout ("" when it can).
func (s *Setup) signInAllowed() string {
	if strings.EqualFold(s.m().Layout.LangID, "0C00") {
		return "a layout of a language without LCID (LANGID 0C00) has a per-user LANGID, so it cannot be put on the sign-in screen or into new accounts"
	}
	if n, err := s.Sys.NativeMachine(); err == nil && n == payload.MachineARM64 {
		return "on Windows on ARM a layout on the sign-in screen is not supported (a reported unbootable machine, inst §2.4)"
	}
	return ""
}

// RecSignInTags is the record value (REG_MULTI_SZ, in the uninstall entry) of the language tags the installer added
// to a §7.1 place, one "<place>=<tag>" each (<place>: .DEFAULT, S-1-5-19, S-1-5-20 or Default; D97).
const RecSignInTags = "YakbdSignInTags"

// recordedTags reads RecSignInTags: place key -> tags.
func (s *Setup) recordedTags() map[string][]string {
	m := map[string][]string{}
	for _, e := range multi(s.values(s.arpKey())[strings.ToLower(RecSignInTags)]) {
		if p, t, ok := strings.Cut(e, "="); ok && validTag(t) {
			m[strings.ToUpper(p)] = append(m[strings.ToUpper(p)], t)
		}
	}
	return m
}

// applySignIn writes the §7.1 entries into every place. On failure it takes back what it wrote in this run, and only
// that (entries that were there before stay). added are the tags it added, "<place>=<tag>".
func (s *Setup) applySignIn(klid string, res *Result) (added []string, err error) {
	lang := s.m().Layout.LangID
	var u undo
	var failed error
	s.filedTag = ""
	if failed = s.systemFiledTag(klid, lang, &u, &added); failed != nil {
		u.run()
		return nil, failed
	}
	for _, p := range s.systemLoaded() {
		if failed = s.applyPlace(p, klid, lang, &u, &added); failed != nil {
			break
		}
	}
	if failed == nil {
		loadErr, e := s.withDefault(res, func(h string) error {
			var du undo
			e := s.applyPlace(place{hive: h}, klid, lang, &du, &added)
			if e != nil {
				du.run() // while the hive is loaded
			}
			return e
		})
		if loadErr != nil {
			e = loadErr
		}
		failed = e
	}
	if failed != nil {
		s.logf("sign-in screen: failed: %v; taking back what this run wrote", failed)
		u.run()
		return nil, failed
	}
	return added, nil
}

// removePlace takes the layout out of one §7.1 place (§9 step 1) and reads the place back: InstallLayoutOrTipUserReg
// with ILOT_UNINSTALL for every "<LLLL>:<KLID>" string found (and the KLID's own LANGID), the values under User
// Profile and User Profile System Backup, and a Substitutes value still naming a KLID with its Preload entry
// (Preload renumbered). A tag the installer recorded as added there is removed from both Languages values with its
// subkeys when no layout value of it is left (D97); other languages stay.
func (s *Setup) removePlace(hive string, set map[string]bool, lang string, tags []string) error {
	r := s.refsIn(hive, set)
	up := Users + `\` + hive + `\Control Panel\International\User Profile`
	backup := Users + `\` + hive + `\Control Panel\International\User Profile System Backup`
	bvals := s.klidValues(backup, set)
	if r.refs || len(bvals) > 0 {
		s.removeFromHive(r, set, "", strings.ToUpper(lang))
		for _, bv := range bvals {
			i := strings.IndexByte(bv, '\\')
			if err := s.Sys.DeleteValue(backup+`\`+bv[:i], bv[i+1:]); err == nil {
				s.logf("hive %s: User Profile System Backup value %s removed", hive, bv)
			}
		}
	} else {
		s.logf("sign-in screen: %s does not name the layout", placeName(hive))
	}
	for _, t := range tags {
		s.dropTag(up, t)
		s.dropTag(backup, t)
	}
	for _, bv := range bvals {
		s.deleteIfEmpty(backup + `\` + bv[:strings.IndexByte(bv, '\\')])
	}
	if len(bvals) > 0 || len(tags) > 0 {
		if len(multi(s.values(backup)["languages"])) == 0 {
			if vs := s.values(backup); len(vs) == 1 && hasValue(vs, "Languages") {
				_ = s.Sys.DeleteValue(backup, "Languages")
			}
		}
		s.deleteIfEmpty(backup)
	}
	if s.refsIn(hive, set).refs || len(s.klidValues(backup, set)) > 0 {
		return fmt.Errorf("%s still names the layout after the removal", placeName(hive))
	}
	s.logf("sign-in screen: %s: the layout removed (read back)", placeName(hive))
	return nil
}

// klidValues lists "<tag>\<LLLL:KLID>" values under key\* naming a KLID of set.
func (s *Setup) klidValues(key string, set map[string]bool) []string {
	var out []string
	tags, _ := s.Sys.SubKeys(key)
	for _, t := range tags {
		for _, v := range s.values(key + `\` + t) {
			if i := strings.IndexByte(v.Name, ':'); i >= 0 && set[strings.ToLower(v.Name[i+1:])] {
				out = append(out, t+`\`+v.Name)
			}
		}
	}
	return out
}

// cleanSignIn is §9 step 1: it takes the layout out of every §7.1 place, refusing first (nothing changed) when it is
// the only layout of the sign-in screen and force is not set, and removes the tags the record names. A Default profile
// that cannot be loaded is skipped with a note; every other failure is returned.
func (s *Setup) cleanSignIn(set map[string]bool, force bool, res *Result) error {
	lang := s.m().Layout.LangID
	sys := s.systemLoaded()
	tags := s.recordedTags()
	// The only-layout refusal (§9 step 1, D94) is made for the sign-in screen only: there a person types.
	only := func(h string) bool { return strings.EqualFold(h, ".DEFAULT") && s.refsIn(h, set).onlyLayout(set) }
	var refused []string
	for _, p := range sys {
		if only(p.hive) {
			refused = append(refused, placeName(p.hive))
		}
	}
	var failed error
	clean := func(p string) {
		if e := s.removePlace(p, set, lang, tags[placeKey(p)]); e != nil && failed == nil {
			failed = e
		}
	}
	loadErr, err := s.withDefault(res, func(h string) error {
		if len(refused) > 0 && !force {
			return nil
		}
		for _, p := range sys {
			clean(p.hive)
		}
		return s.removePlace(h, set, lang, tags["DEFAULT"])
	})
	if len(refused) > 0 && !force {
		return fail(ExitRefused, "The layout is the only keyboard layout of %s. Add another layout there first (or run the uninstaller with FORCE=1). Nothing was changed.", strings.Join(refused, ", "))
	}
	if loadErr != nil {
		// Stale entries there are ignored at sign-in and by accounts made from it (inst §4.7 [VM]); the rest goes on.
		res.note("The Default profile (for new accounts) could not be opened (%v); if the layout was there, it stays there unused.", loadErr)
		for _, p := range sys {
			clean(p.hive)
		}
	}
	if failed == nil {
		failed = err
	}
	if failed != nil {
		return fail(ExitFailed, "Removing the layout from the sign-in screen and the Default profile failed: %v", failed)
	}
	if len(tags) > 0 {
		_ = s.Sys.DeleteValue(s.arpKey(), RecSignInTags)
	}
	return nil
}

// SignInStep applies rule enableForSignInAndNewUsers after registration and the uninstall entry, and after the
// user's enabling when this process is the user's own account (§7.1 "Order", D97): a fresh install follows the
// manifest unless SignIn overrides it; an upgrade or repair keeps the recorded state unless SignIn overrides it, and
// SIGNIN=0 then removes the layout from the places. It records the result in the uninstall entry. A failure is not an
// error of the run: it is set in res.SignInFailed (the run ends with the enabling failure code).
func (s *Setup) SignInStep(res *Result) {
	fresh := !res.Upgraded
	rec := s.values(s.arpKey())[strings.ToLower(RecSignIn)].Str == "1"
	want, why := s.m().SignIn, "the installer's setting"
	if !fresh {
		want, why = rec, "the recorded state"
	}
	if s.SignIn != nil {
		want, why = *s.SignIn, "SIGNIN on the command line"
	}
	record := func(on bool) {
		v := map[bool]string{true: "1", false: "0"}[on]
		if err := s.Sys.SetString(s.arpKey(), RecSignIn, v, false); err != nil {
			s.logf("cannot write %s: %v", RecSignIn, err)
		}
	}
	set := map[string]bool{strings.ToLower(res.KLID): true}
	if !want {
		s.logf("sign-in screen and new users: off (%s)", why)
		if s.SignIn != nil && !fresh {
			if err := s.cleanSignIn(set, false, res); err != nil {
				s.logf("sign-in screen: removal failed: %v", err)
				res.SignInFailed = err
				res.Notes = append(res.Notes, err.Error())
				return
			}
			res.note("The layout was removed from the sign-in screen and from the profile for new accounts.")
		}
		if fresh && !rec {
			// A plain install writes no record value: the record says "0" when it has none.
			return
		}
		record(false)
		return
	}
	if no := s.signInAllowed(); no != "" {
		s.logf("sign-in screen and new users: not applied: %s", no)
		res.note("The layout was not put on the sign-in screen or into new accounts: %s.", no)
		record(false)
		return
	}
	s.logf("sign-in screen and new users: on (%s)", why)
	added, err := s.applySignIn(res.KLID, res)
	if errors.Is(err, errNoLanguage) {
		res.SignInFailed = fail(ExitEnable, "The layout is installed, but the sign-in screen and the profile for new accounts do not have its language, and no signed-in user has it, so the installer cannot tell which language Windows files it under. Nothing was put there. Add the layout to a user's input list (Settings > Time & language > Language & region), then run the installer again with /f SIGNIN=1.")
	} else if err != nil {
		res.SignInFailed = fail(ExitEnable, "The layout is installed, but putting it on the sign-in screen and into the profile for new accounts failed: %v. Nothing was left there.", err)
	}
	if res.SignInFailed != nil {
		res.Notes = append(res.Notes, res.SignInFailed.Error())
		record(false)
		return
	}
	record(true)
	if len(added) > 0 {
		var all []string
		for p, ts := range s.recordedTags() {
			for _, t := range ts {
				all = append(all, p+"="+t)
			}
		}
		for _, a := range added {
			if !containsFold(all, a) {
				all = append(all, a)
			}
		}
		sort.Strings(all)
		if err := s.Sys.SetMultiString(s.arpKey(), RecSignInTags, all); err != nil {
			s.logf("cannot write %s: %v", RecSignInTags, err)
		}
	}
	msg := "The layout is on the sign-in screen and in the profile for new accounts; a restart may be needed before the sign-in screen shows it."
	if len(added) > 0 {
		msg += " Its language was added there too."
	}
	res.note("%s", msg)
}

// deleteIfEmpty deletes a key that has neither values nor subkeys (a System Backup key left empty by a removal).
func (s *Setup) deleteIfEmpty(key string) {
	vs, err := s.Sys.Values(key)
	if err != nil || len(vs) > 0 {
		return
	}
	if subs, _ := s.Sys.SubKeys(key); len(subs) > 0 {
		return
	}
	if err := s.Sys.DeleteKey(key); err == nil {
		s.logf("%s: empty, deleted", key)
	}
}

// LocalSystemSID is the SID of LocalSystem, whose hive is HKU\.DEFAULT.
const LocalSystemSID = "S-1-5-18"

// systemFiledTag is the LocalSystem route of D98 (Q14): when this process runs as LocalSystem, no loaded user hive has
// the layout's language and .DEFAULT does not have it either, InstallLayoutOrTip("<LLLL>:<KLID>", 0) as LocalSystem
// files the language in .DEFAULT under Windows' own tag (VM-26). The tag is read back and used for every place
// (s.filedTag); .DEFAULT gets the System Backup part of the D97 shape, which the call does not write, and the tag is
// recorded as added there, so that uninstall and rollback remove it from .DEFAULT's Languages again.
func (s *Setup) systemFiledTag(klid, lang string, u *undo, added *[]string) error {
	if sid, _ := s.Sys.UserSID(); !strings.EqualFold(sid, LocalSystemSID) {
		return nil
	}
	up := userProfile(".DEFAULT")
	if s.languageTag(up, lang) != "" || s.userTag(lang) != "" {
		return nil
	}
	str := layoutString(lang, klid)
	err := s.Sys.InstallLayoutOrTip(str, 0)
	s.logf("sign-in screen: as LocalSystem, no user has LANGID %s: InstallLayoutOrTip(%q, 0): %v", strings.ToUpper(lang), str, errText(err))
	set := map[string]bool{strings.ToLower(klid): true}
	tag := s.languageTag(up, lang)
	u.push(func() {
		var tags []string
		if tag != "" {
			tags = []string{tag}
		}
		_ = s.removePlace(".DEFAULT", set, lang, tags)
	})
	if err != nil {
		return fmt.Errorf("InstallLayoutOrTip(%q) as LocalSystem failed: %v", str, err)
	}
	if tag == "" {
		return errNoLanguage
	}
	s.logf("sign-in screen: Windows filed LANGID %s as %s in .DEFAULT", strings.ToUpper(lang), tag)
	s.filedTag = tag
	*added = append(*added, ".DEFAULT="+tag)
	bk := Users + `\.DEFAULT\Control Panel\International\User Profile System Backup`
	return s.addTag(bk, tag, u)
}
