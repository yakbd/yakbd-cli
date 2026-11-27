package setup

import (
	"fmt"
	"strings"
)

// The custom-language route of kbl targets/windows-install.md section 6 (inst §6 requirement 2; confirmed in
// yakbd VM run 2, D63): a layout with LANGID 0C00 is enabled through the tag registration.localeName and the
// transient LANGID Windows gives that tag. The installer records in the user's hive that it added the tag, and the
// uninstaller removes the tag again only then. Windows gives a new tag a default input method of its own; it is
// left alone and goes with the tag.

// RecTag is the value, under HKU\<SID>\Software\YAKBD\<product code>, that names a tag the installer added.
const RecTag = "AddedLanguageTag"

func userProfile(sid string) string {
	return Users + `\` + sid + `\Control Panel\International\User Profile`
}

func (s *Setup) tagRecordKey(sid string) string {
	return Users + `\` + sid + `\Software\YAKBD\` + s.m().Product.Code
}

func validTag(t string) bool {
	if t == "" || len(t) > 85 {
		return false
	}
	for _, c := range t {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func multi(v Value) []string {
	var out []string
	for _, x := range strings.Split(v.Str, "\x00") {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

// languages returns the user's language list (User Profile\Languages).
func (s *Setup) languages(sid string) []string {
	return multi(s.values(userProfile(sid))["languages"])
}

func containsFold(list []string, t string) bool {
	for _, x := range list {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}

// customLangID adds the tag when the user lacks it and returns its transient LANGID as 4 hex digits.
func (s *Setup) customLangID(sid string) (string, error) {
	tag := s.m().Layout.LocaleName
	if !validTag(tag) {
		return "", fail(ExitEnable, "The layout is installed, but its language tag %q cannot be added to your list. Add the layout in Settings > Time & language > Language & region.", tag)
	}
	if !containsFold(s.languages(sid), tag) {
		if err := s.Sys.AddLanguageTag(tag); err != nil {
			return "", fail(ExitEnable, "The layout is installed, but adding the language %s to your list failed (%v). Add it in Settings > Time & language > Language & region.", tag, err)
		}
		k := s.tagRecordKey(sid)
		if _, err := s.Sys.CreateKey(k); err == nil {
			_ = s.Sys.SetString(k, RecTag, tag, false)
		}
		s.logf("enable: language %s added to the list of %s (recorded in %s)", tag, sid, k)
	} else {
		s.logf("enable: language %s is in the list already", tag)
	}
	up := userProfile(sid)
	subs, _ := s.Sys.SubKeys(up)
	for _, t := range subs {
		if !strings.EqualFold(t, tag) {
			continue
		}
		if v, ok := s.values(up + `\` + t)["transientlangid"]; ok && v.Type == RegDWORD && v.DWord != 0 {
			id := fmt.Sprintf("%04X", v.DWord&0xFFFF)
			s.logf("enable: %s has the transient LANGID %s", tag, id)
			return id, nil
		}
	}
	return "", fail(ExitEnable, "The layout is installed, but Windows gave the language %s no LANGID (too many such languages?). Add the layout in Settings > Time & language > Language & region.", tag)
}

// removeAddedTags removes, from every loaded user hive whose record names it, the tag the installer added
// (kbl targets/windows-install.md section 9): the user's own list through RemoveLanguageTag, another user's by
// editing Languages and deleting the tag's User Profile key (inst §10 fallback for other hives).
func (s *Setup) removeAddedTags(sid string) {
	for _, h := range s.hives() {
		if systemHive(h) {
			continue
		}
		k := s.tagRecordKey(h)
		tag := s.values(k)[strings.ToLower(RecTag)].Str
		if !validTag(tag) {
			if tag != "" {
				s.logf("hive %s: recorded tag %q is not a language tag; left alone", h, tag)
			}
			continue
		}
		if strings.EqualFold(h, sid) {
			err := s.Sys.RemoveLanguageTag(tag)
			s.logf("hive %s (this user): language %s removed from the list: %v", h, tag, errText(err))
		} else {
			up := userProfile(h)
			var keep []string
			for _, l := range s.languages(h) {
				if !strings.EqualFold(l, tag) {
					keep = append(keep, l)
				}
			}
			_ = s.Sys.SetMultiString(up, "Languages", keep)
			subs, _ := s.Sys.SubKeys(up)
			for _, t := range subs {
				if strings.EqualFold(t, tag) {
					err := s.Sys.DeleteKey(up + `\` + t)
					s.logf("hive %s: language %s removed by hand: %v", h, tag, errText(err))
				}
			}
		}
		_ = s.Sys.DeleteKey(k)
		_ = s.Sys.DeleteKey(Users + `\` + h + `\Software\YAKBD`) // only when empty
	}
}
