package build

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows"
)

// drops reports the general data a Windows build does not carry (SPEC §17.3, targets/windows.md §8, §11).
func (b *lowerer) drops() {
	l := b.l
	if l.Hardware == nil && len(l.Touch) == 0 {
		b.lost("general.noLayers", "", "the file has neither hardware nor touch layers")
	}
	if l.Hardware == nil {
		b.lost("windows.noHardwareLayers", "/hardware", "the file has no hardware layers: the DLL would hold only Table A's default rows (D89)")
	}
	if len(l.Touch) > 0 || len(l.Flicks) > 0 {
		b.lost("general.touchDropped", "/touch", "touch layouts and gestures are not part of a Windows layout")
	} else {
		// Long-press and multi-tap choices of keys are gestures too (SPEC §17.3), also on a keyboard without touch
		// layers (CLDR fr.xml: super-2).
		for _, id := range sortedKeys(l.Keys) {
			if k := l.Keys[id]; len(k.LongPressKeyIDs) > 0 || len(k.MultiTapKeyIDs) > 0 {
				b.lost("general.touchDropped", "/keys/"+model.Escape(id), "long-press and multi-tap choices are not part of a Windows layout")
				break
			}
		}
	}
	if l.Displays != nil {
		b.lost("general.displayDropped", "/displays", "displays are not part of a Windows layout")
	}
	if len(l.Tests) > 0 {
		b.lost("general.testsDropped", "/tests", "tests are not part of a Windows layout")
	}
	if len(l.Extensions) > 0 || len(l.CLDRSpecial) > 0 {
		b.lost("general.extensionDropped", "/extensions", "extensions and CLDR special elements are not part of a Windows layout")
	}
	if t := l.Transforms; t != nil {
		for i := range t.Simple {
			b.lost("windows.transformNotCompiled", fmt.Sprintf("/transforms/simple/%d", i), "transforms are not compiled")
		}
		for i := range t.Backspace {
			b.lost("windows.transformNotCompiled", fmt.Sprintf("/transforms/backspace/%d", i), "backspace transforms are not compiled")
		}
	}
	if b.file.NLS != nil {
		b.pin.NLS = b.file.NLS
		b.lost("windows.nlsNotCompiled", "/targets/windows/nls", "the NLS table is carried, not built")
	}
	if b.file.Dispatcher != nil {
		b.pin.Dispatcher = b.file.Dispatcher
		b.lost("windows.dispatcherNotCompiled", "/targets/windows/dispatcher", "the dispatcher data is carried, not built")
	}
	if b.norm {
		b.lost("general.normalizationNotApplied", "/settings/normalization", "each output is NFC-normalised at build time; text that several keystrokes make together is not (Windows does not normalise while typing; D73)")
	}
}

// localeOf is rule strings' localeName: metadata.id up to its first singleton extension.
func localeOf(id string) string {
	parts := strings.Split(id, "-")
	for i, p := range parts {
		if i > 0 && len(p) == 1 {
			return strings.Join(parts[:i], "-")
		}
	}
	return id
}

// resources decides strings, versionInfo and registration (§2, §9) and builds the DLL value.
func (b *lowerer) resources() *windows.DLL {
	l, f, p := b.l, b.file, b.pin
	reg := &model.Registration{}
	if f.Registration != nil {
		*reg = *f.Registration
	}
	langID := reg.LangID
	if langID == "" {
		b.lost("windows.langIdMissing", "/targets/windows/registration/langId", "no LANGID: set targets.windows.registration.langId")
	}
	st := &model.WindowsStrings{}
	if f.Strings != nil {
		*st = *f.Strings
	}
	var defaulted []string
	if st.LayoutName == nil && langID != "" {
		st.LayoutName = map[string]string{langID: l.Metadata.Name}
		defaulted = append(defaulted, "layoutName")
	}
	if st.LanguageName == nil && langID != "" {
		st.LanguageName = map[string]string{langID: strings.Split(l.Metadata.ID, "-")[0]}
		defaulted = append(defaulted, "languageName")
	}
	if st.LocaleName == nil {
		v := localeOf(l.Metadata.ID)
		st.LocaleName = &v
		defaulted = append(defaulted, "localeName")
	}
	if len(defaulted) > 0 {
		b.defaulted("strings", memberFields("strings", defaulted), defaulted, "DLL strings %s by rule", strings.Join(defaulted, ", "))
	}
	if len(l.Metadata.LocalizedNames) > 0 && (f.Strings == nil || f.Strings.LayoutName == nil) {
		b.lost("windows.stringLanguageUnmapped", "/metadata/localizedNames", "localised names have no LANGID; pin targets.windows.strings.layoutName")
	}
	p.Strings = st
	d := &windows.DLL{FileName: strings.ToLower(p.DLLName) + ".dll", Tables: b.t}
	for _, x := range []struct {
		id uint16
		m  map[string]string
	}{{windows.StringLayoutName, st.LayoutName}, {windows.StringLanguageName, st.LanguageName}} {
		for _, k := range sortedKeys(x.m) {
			lang, _ := strconv.ParseUint(k, 16, 16)
			d.Strings = append(d.Strings, windows.String{ID: x.id, Lang: uint16(lang), Text: x.m[k]})
		}
	}
	lang := uint16(0x0409)
	if v, err := strconv.ParseUint(langID, 16, 16); err == nil {
		lang = uint16(v)
	}
	if *st.LocaleName != "" {
		d.Strings = append(d.Strings, windows.String{ID: windows.StringLocaleName, Lang: lang, Text: *st.LocaleName})
	}

	vi := &model.VersionInfo{}
	if f.VersionInfo != nil {
		*vi = *f.VersionInfo
	}
	var vdef []string
	if vi.FileVersion == "" {
		vi.FileVersion = b.versionOf(l.Metadata.Version)
		vdef = append(vdef, "fileVersion")
	}
	if vi.ProductVersion == "" {
		vi.ProductVersion = vi.FileVersion
		vdef = append(vdef, "productVersion")
	}
	if vi.CompanyName == nil && l.Metadata.Organization != "" {
		v := l.Metadata.Organization
		vi.CompanyName = &v
		vdef = append(vdef, "companyName")
	}
	if vi.LegalCopyright == nil && l.Metadata.Copyright != "" {
		v := l.Metadata.Copyright
		vi.LegalCopyright = &v
		vdef = append(vdef, "legalCopyright")
	}
	if vi.FileDescription == nil {
		v := l.Metadata.Name
		vi.FileDescription = &v
		vdef = append(vdef, "fileDescription")
	}
	if len(vdef) > 0 {
		b.defaulted("versionInfo", memberFields("versionInfo", vdef), vdef, "version resource %s by rule", strings.Join(vdef, ", "))
	}
	p.VersionInfo = vi
	str := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	d.Version = windows.DefaultVersion(d.FileName, str(vi.FileDescription), str(vi.CompanyName), str(vi.LegalCopyright), lang, b.opt.ToolVersion)
	fv := parseVersion4(vi.FileVersion)
	d.Version.FileVersion = fv
	d.Version.ProductVersion = parseVersion4(vi.ProductVersion)
	for i, kv := range d.Version.Strings {
		switch kv.Key {
		case "FileVersion":
			d.Version.Strings[i].Value = vi.FileVersion
		case "ProductVersion":
			// The tool's own version stays the product version string of the resource's ProductName.
		}
	}

	var rdef []string
	if reg.LayoutText == "" {
		reg.LayoutText = l.Metadata.Name
		rdef = append(rdef, "layoutText")
	}
	if strings.EqualFold(langID, "0C00") {
		if reg.LocaleName == "" {
			reg.LocaleName = *st.LocaleName
			rdef = append(rdef, "localeName")
		}
		if reg.CustomLanguageName == "" && langID != "" {
			reg.CustomLanguageName = st.LanguageName[langID]
			rdef = append(rdef, "customLanguageName")
		}
	}
	if len(rdef) > 0 {
		b.defaulted("registration", memberFields("registration", rdef), rdef, "registration %s by rule", strings.Join(rdef, ", "))
	}
	p.Registration = reg
	return d
}

// versionOf is rule versionInfo's version: the first three numbers of metadata.version and .0.
func (b *lowerer) versionOf(v string) string {
	parts := strings.SplitN(v, ".", 4)
	nums := []int{0, 0, 0}
	for i := 0; i < 3 && i < len(parts); i++ {
		p := parts[i]
		if j := strings.IndexAny(p, "-+"); j >= 0 {
			p = p[:j]
		}
		n, err := strconv.Atoi(p)
		if v != "" && (err != nil || n > 65535 || n < 0) {
			b.lost("windows.versionUnrepresentable", "/metadata/version", "metadata.version %q does not fit four 16-bit numbers", v)
			n = 0
		}
		nums[i] = n
	}
	return fmt.Sprintf("%d.%d.%d.0", nums[0], nums[1], nums[2])
}

func parseVersion4(s string) [4]uint16 {
	var out [4]uint16
	for i, p := range strings.SplitN(s, ".", 4) {
		n, _ := strconv.Atoi(p)
		out[i] = uint16(n)
	}
	return out
}
