package setup

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/yakbd/yakbd-cli/installer/cmdline"
)

// undo is the rollback list of one run, applied in reverse (inst §3.4, §7).
type undo []func()

func (u *undo) push(f func()) { *u = append(*u, f) }

func (u undo) run() {
	for i := len(u) - 1; i >= 0; i-- {
		u[i]()
	}
}

// Install installs or, when the product is installed already, upgrades or repairs it in place (docs/installer.md
// §2.1, §2.2). It does not enable the layout for anyone; Enable does.
func (s *Setup) Install() (*Result, error) {
	if err := s.requireAdmin(); err != nil {
		return nil, err
	}
	pl, native, err := s.placements()
	if err != nil {
		return nil, err
	}
	m := s.m()
	s.logf("install %s %s (%s), product %s, machine %#04x, files %v", m.Product.Name, m.Product.Version, m.Layout.File, m.Product.Code, native, pl)
	if err := s.checkFiles(pl); err != nil {
		return nil, err
	}

	ks, err := s.scanKLIDs()
	if err != nil {
		return nil, err
	}
	var owned []klidInfo
	for _, k := range ks {
		if strings.EqualFold(k.product, m.Product.Code) {
			owned = append(owned, k)
		}
	}
	rec, hasRec := s.readRecord()
	if hasRec {
		// Same product code: an upgrade, a repair of the same version, or a downgrade, which is refused as Windows
		// Installer refuses it (ERROR_PRODUCT_VERSION; docs/installer-cli.md §6).
		if v := s.values(s.arpKey())["displayversion"].Str; cmdline.CompareVersions(v, m.Product.Version) > 0 {
			return nil, fail(ExitNewer, "Version %s of \"%s\" is installed, newer than this installer's %s. To go back, uninstall it first. Nothing was changed.", v, m.Product.Name, m.Product.Version)
		}
	}
	if len(owned) > 0 || hasRec {
		return s.upgrade(pl, ks, owned, rec, hasRec)
	}

	res := &Result{}
	var u undo
	failRollback := func(err error) (*Result, error) {
		s.logf("failed: %v; rolling back", err)
		u.run()
		return nil, err
	}

	// D62: a target that an earlier uninstall left for deletion at the next restart is a refusal.
	if p := s.pendingDeletion(); p != "" {
		return nil, fail(ExitFailed, "%s will be deleted when Windows restarts (an earlier uninstall). Restart Windows, then run the installer again. Nothing was changed.", p)
	}

	// 1. Files.
	for _, p := range pl {
		created, err := s.placeFile(p, false, res)
		if created != "" {
			path := created
			u.push(func() { s.logf("rollback: delete %s", path); _ = s.Sys.Remove(path) })
		}
		if err != nil {
			return failRollback(err)
		}
	}

	// 2. KLID and Layout Id.
	langID := strings.ToUpper(m.Layout.LangID)
	allUsed, perLang := usedIDs(ks, "")
	var id uint16
	if m.Layout.LayoutID != "" {
		id = hex16(m.Layout.LayoutID) & 0xFFF
		if perLang[strings.ToLower(langID)][id] {
			return failRollback(fail(ExitFailed, "The layout's Layout Id %s is used by another layout of language %s. Nothing was changed.", m.Layout.LayoutID, langID))
		}
	} else {
		var ok bool
		if id, ok = AllocateLayoutID(allUsed); !ok {
			return failRollback(fail(ExitFailed, "No free Layout Id between 00c0 and 00ff. Nothing was changed."))
		}
	}
	klid, err := s.createKLID(langID)
	if err != nil {
		return failRollback(err)
	}
	u.push(func() { s.logf("rollback: delete KLID %s", klid); _ = s.Sys.DeleteKey(klidKey(klid)) })
	if err := s.writeValues(klid, id); err != nil {
		return failRollback(err)
	}
	res.KLID, res.LayoutID = klid, fmt.Sprintf("%04x", id)

	// 3. Uninstall entry.
	if err := s.writeEntry(res, &u); err != nil {
		return failRollback(err)
	}
	s.logf("installed: KLID %s, Layout Id %s", res.KLID, res.LayoutID)
	return res, nil
}

// placeFile writes one file (inst §10 step 1). It returns the path when this run created the file. A file of the
// name that differs is foreign and refused, unless owned is set (upgrade): then it is replaced, at the next
// restart when it is in use.
func (s *Setup) placeFile(p Placement, owned bool, res *Result) (string, error) {
	target, err := s.targetPath(p.Dir)
	if err != nil {
		return "", err
	}
	data := s.P.Data[p.Build]
	old, err := s.Sys.ReadFile(target)
	switch {
	case err == nil && !owned:
		return "", fail(ExitFailed, "A file %s exists already and is not this layout's; it may belong to Windows or another program, so it is not overwritten. Nothing was changed.", target)
	case err == nil && bytes.Equal(old, data):
		s.logf("%s: already present and identical", target)
		return "", nil
	case err != nil && !errors.Is(err, ErrNotFound):
		return "", fmt.Errorf("cannot read %s: %v", target, err)
	}
	tmp := target + ".yakbd-tmp"
	if err := s.Sys.WriteFile(tmp, data); err != nil {
		return "", fmt.Errorf("cannot write %s: %v", tmp, err)
	}
	if err := s.Sys.Rename(tmp, target); err != nil {
		if owned && errors.Is(err, ErrInUse) {
			next := target + ".yakbd-new"
			_ = s.Sys.Remove(tmp)
			if err := s.Sys.WriteFile(next, data); err != nil {
				return "", fmt.Errorf("cannot write %s: %v", next, err)
			}
			if err := s.Sys.MoveAtReboot(next, target); err != nil {
				return "", fmt.Errorf("cannot schedule the replacement of %s: %v", target, err)
			}
			s.logf("%s is in use: replaced at the next restart", target)
			res.Reboot = true
			return "", nil
		}
		_ = s.Sys.Remove(tmp)
		return "", fmt.Errorf("cannot write %s: %v", target, err)
	}
	back, err := s.Sys.ReadFile(target)
	if err != nil || !bytes.Equal(back, data) {
		return target, fmt.Errorf("%s did not read back as written", target)
	}
	if err := s.checkFile(p.Build, back); err != nil {
		return target, fmt.Errorf("%s as written: %v", target, err)
	}
	s.logf("wrote %s (%s, %d bytes)", target, p.Build, len(data))
	if old != nil {
		return "", nil
	}
	return target, nil
}

// createKLID creates the KLID key atomically (inst §3.1, §10 step 2).
func (s *Setup) createKLID(langID string) (string, error) {
	m := s.m()
	if m.Layout.KLID != "" {
		klid := strings.ToLower(m.Layout.KLID)
		created, err := s.Sys.CreateKey(klidKey(klid))
		if err != nil {
			return "", fmt.Errorf("cannot create KLID %s: %v", klid, err)
		}
		if !created {
			return "", fail(ExitFailed, "The layout's KLID %s is used by another layout. Nothing was changed.", klid)
		}
		return klid, nil
	}
	for hi := 0xA000; hi <= 0xAFFE; hi++ {
		klid := fmt.Sprintf("%04x%s", hi, strings.ToLower(langID))
		if s.referenced(klid) {
			s.logf("KLID %s skipped: a user profile still refers to it", klid)
			continue
		}
		created, err := s.Sys.CreateKey(klidKey(klid))
		if err != nil {
			return "", fmt.Errorf("cannot create KLID %s: %v", klid, err)
		}
		if created {
			return klid, nil
		}
	}
	return "", fail(ExitFailed, "No free KLID for language %s. Nothing was changed.", langID)
}

// writeValues writes the values of inst §3.2 / §10 step 2, Layout File first.
func (s *Setup) writeValues(klid string, id uint16) error {
	m := s.m()
	k := klidKey(klid)
	dll := m.Layout.File
	type sv struct {
		name, value string
		expand      bool
	}
	vals := []sv{
		{"Layout File", dll, false},
		{"Layout Text", m.Layout.LayoutText, false},
		{"Layout Display Name", `@%SystemRoot%\system32\` + dll + `,-1000`, true},
		{"Layout Id", fmt.Sprintf("%04x", id), false},
		{"Layout Product Code", m.Product.Code, false},
	}
	if m.Layout.CustomLanguageName != "" || strings.EqualFold(m.Layout.LangID, "0C00") {
		name := m.Layout.CustomLanguageName
		if name == "" {
			name = m.Layout.LayoutText
		}
		vals = append(vals, sv{"Custom Language Name", name, false}, sv{"Custom Language Display Name", `@%SystemRoot%\system32\` + dll + `,-1100`, true})
	}
	if strings.EqualFold(m.Layout.LangID, "0C00") && m.Layout.LocaleName != "" {
		vals = append(vals, sv{"Layout Locale Name", m.Layout.LocaleName, false})
	}
	for _, v := range vals {
		if err := s.Sys.SetString(k, v.name, v.value, v.expand); err != nil {
			return fmt.Errorf("cannot write %s\\%s: %v", k, v.name, err)
		}
	}
	s.logf("KLID %s: values written (Layout Id %04x)", klid, id)
	return nil
}

// writeEntry copies the installer into Program Files and writes the uninstall entry (docs/installer.md §2.3).
func (s *Setup) writeEntry(res *Result, u *undo) error {
	m := s.m()
	dir, err := s.installDir()
	if err != nil {
		return err
	}
	exe := joinPath(dir, "setup.exe")
	self := s.Sys.SelfPath()
	selfBytes, err := s.Sys.ReadFile(self)
	if err != nil {
		return fmt.Errorf("cannot read the installer itself (%s): %v", self, err)
	}
	if !samePath(self, exe) {
		if err := s.Sys.MkdirAll(dir); err != nil {
			return fmt.Errorf("cannot create %s: %v", dir, err)
		}
		if err := s.Sys.WriteFile(exe, selfBytes); err != nil {
			return fmt.Errorf("cannot write %s: %v", exe, err)
		}
		u.push(func() {
			_ = s.Sys.Remove(exe)
			_ = s.Sys.RemoveDir(dir)
		})
		s.logf("uninstaller: %s", exe)
	}
	key := s.arpKey()
	created, err := s.Sys.CreateKey(key)
	if err != nil {
		return fmt.Errorf("cannot create the uninstall entry %s: %v", key, err)
	}
	if created {
		u.push(func() { _ = s.Sys.DeleteKey(key) })
	}
	size := uint32(len(selfBytes))
	for _, d := range s.P.Data {
		size += uint32(len(d))
	}
	strs := [][2]string{
		{"DisplayName", m.Product.Name},
		{"DisplayVersion", m.Product.Version},
		{"InstallLocation", dir},
		// msiexec-compatible command lines (docs/installer-cli.md §7); NoModify and NoRepair stay unset because
		// repair is supported, and WindowsInstaller is never written (this is not an MSI product).
		{"UninstallString", `"` + exe + `" /x ` + m.Product.Code},
		{"QuietUninstallString", `"` + exe + `" /x ` + m.Product.Code + ` /quiet /norestart`},
		{"ModifyPath", `"` + exe + `" /f ` + m.Product.Code},
		{"DisplayIcon", exe},
		{RecKLID, res.KLID},
		{RecLayoutID, res.LayoutID},
		{RecFile, m.Layout.File},
		{RecLangID, strings.ToUpper(m.Layout.LangID)},
	}
	if m.Product.Publisher != "" {
		strs = append(strs, [2]string{"Publisher", m.Product.Publisher})
	}
	for _, v := range strs {
		if err := s.Sys.SetString(key, v[0], v[1], false); err != nil {
			return fmt.Errorf("cannot write %s\\%s: %v", key, v[0], err)
		}
	}
	if err := s.Sys.SetDWORD(key, "EstimatedSize", (size+1023)/1024); err != nil {
		return fmt.Errorf("cannot write %s\\EstimatedSize: %v", key, err)
	}
	s.logf("uninstall entry %s written", key)
	return nil
}

// upgrade is UPGRADE_IN_PLACE / REPAIR of inst §10: the same KLID and Layout Id, files replaced, values rewritten.
func (s *Setup) upgrade(pl []Placement, ks []klidInfo, owned []klidInfo, rec record, hasRec bool) (*Result, error) {
	m := s.m()
	res := &Result{Upgraded: true}
	klid := ""
	switch {
	case hasRec:
		klid = strings.ToLower(rec.klid)
	default:
		klid = strings.ToLower(owned[0].name)
	}
	present := false
	var cur klidInfo
	for _, k := range ks {
		if strings.EqualFold(k.name, klid) {
			present, cur = true, k
		}
	}
	if present && !strings.EqualFold(cur.product, m.Product.Code) {
		return nil, fail(ExitFailed, "The recorded KLID %s now belongs to another layout. Uninstall this layout first.", klid)
	}
	if len(klid) != 8 || !strings.EqualFold(klid[4:], m.Layout.LangID) {
		return nil, fail(ExitFailed, "The installed layout (KLID %s) is filed under another language than this installer's (%s). Uninstall it first.", klid, m.Layout.LangID)
	}
	if m.Layout.KLID != "" && !strings.EqualFold(m.Layout.KLID, klid) {
		return nil, fail(ExitFailed, "The installed layout has KLID %s, this installer pins %s. Uninstall it first.", klid, m.Layout.KLID)
	}
	if hasRec && rec.file != "" && !strings.EqualFold(rec.file, m.Layout.File) {
		return nil, fail(ExitFailed, "The installed layout uses the file %s, this installer %s. Uninstall it first.", rec.file, m.Layout.File)
	}
	s.logf("installed already as KLID %s: upgrade in place", klid)

	// Layout Id: kept, unless another KLID took it meanwhile (then the HKL changes, inst §4.5).
	_, perLang := usedIDs(ks, klid)
	var id uint16
	switch {
	case present && cur.hasID:
		id = cur.layoutID
	case hasRec && rec.layoutID != "":
		id = hex16(rec.layoutID) & 0xFFF
	case m.Layout.LayoutID != "":
		id = hex16(m.Layout.LayoutID) & 0xFFF
	}
	if id == 0 || perLang[strings.ToLower(klid[4:])][id] {
		all, _ := usedIDs(ks, klid)
		nid, ok := AllocateLayoutID(all)
		if !ok {
			return nil, fail(ExitFailed, "No free Layout Id between 00c0 and 00ff.")
		}
		if id != 0 {
			// The KLID stays, so every user's entries (Preload, Substitutes, User Profile) still name the layout;
			// only the HKL changes (inst §3.2, §4.5). The user's own process enables it again (Reenable).
			s.logf("Layout Id %04x is taken by another layout of language %s: new Layout Id %04x", id, strings.ToUpper(klid[4:]), nid)
			for _, h := range s.hives() {
				if !systemHive(h) && s.refsIn(h, map[string]bool{klid: true}).refs {
					s.logf("hive %s has the layout in its list", h)
				}
			}
			res.note("Windows gave the layout's Layout Id %04x to another layout; the layout now has %04x. Other users who had the layout keep it in their list; if it does not work for them, they sign out and in again.", id, nid)
		}
		id = nid
	}

	for _, p := range pl {
		if _, err := s.placeFile(p, true, res); err != nil {
			return nil, err
		}
	}
	if !present {
		created, err := s.Sys.CreateKey(klidKey(klid))
		if err != nil {
			return nil, fmt.Errorf("cannot create KLID %s: %v", klid, err)
		}
		if !created {
			return nil, fail(ExitFailed, "KLID %s appeared while repairing; run the installer again.", klid)
		}
		s.logf("KLID %s was missing: registered again (repair)", klid)
	}
	if err := s.writeValues(klid, id); err != nil {
		return nil, err
	}
	res.KLID, res.LayoutID = klid, fmt.Sprintf("%04x", id)
	var u undo // an upgrade that fails half-way is repaired by running it again; nothing is rolled back
	if err := s.writeEntry(res, &u); err != nil {
		return nil, err
	}
	if res.Reboot {
		res.note("A layout file is in use; restart Windows to finish.")
	}
	s.logf("upgraded: KLID %s, Layout Id %s", res.KLID, res.LayoutID)
	return res, nil
}

// Enable adds the installed layout to the input list of the user this process runs as (inst §4.3, §10 step 3).
// It needs no elevation. The layout must be installed (its uninstall entry is read).
func (s *Setup) Enable() (*Result, error) {
	rec, ok := s.readRecord()
	if !ok {
		return nil, fail(ExitEnable, "The layout is not installed; it cannot be enabled.")
	}
	res := &Result{KLID: rec.klid, LayoutID: rec.layoutID}
	lang := strings.ToUpper(rec.langID)
	if lang == "" {
		lang = strings.ToUpper(s.m().Layout.LangID)
	}
	sid, _ := s.Sys.UserSID()
	if lang == "0C00" {
		t, err := s.customLangID(sid)
		if err != nil {
			return nil, err
		}
		lang = t
	}
	str := lang + ":" + strings.ToUpper(rec.klid)
	if err := s.addToList(sid, rec.klid, str); err != nil {
		return nil, err
	}
	hkl := HKL(hex16(lang), hex16(rec.layoutID))
	if !s.loaded(hkl) {
		res.note("The layout was added to your input list; if it does not appear, sign out and sign in again.")
	} else {
		s.logf("enable: HKL %08x is loaded", hkl)
	}
	return res, nil
}

// SessionManager holds PendingFileRenameOperations, the list MoveFileExW with MOVEFILE_DELAY_UNTIL_REBOOT writes:
// pairs of source and target, an empty target meaning "delete".
const SessionManager = `HKLM\SYSTEM\CurrentControlSet\Control\Session Manager`

// pendingDeletion returns the pending entry that deletes the layout's file from a system directory, or "".
func (s *Setup) pendingDeletion() string {
	file := `\` + strings.ToLower(s.m().Layout.File)
	for _, v := range s.values(SessionManager) {
		if !strings.EqualFold(v.Name, "PendingFileRenameOperations") || v.Type != RegMultiSZ {
			continue
		}
		parts := strings.Split(v.Str, "\x00")
		for i := 0; i+1 < len(parts); i += 2 {
			src := strings.ToLower(parts[i])
			if parts[i+1] == "" && strings.HasSuffix(src, file) &&
				(strings.Contains(src, `\system32\`) || strings.Contains(src, `\syswow64\`) || strings.Contains(src, `\sysnative\`)) {
				return parts[i]
			}
		}
	}
	return ""
}

// inProfile reports whether the user's User Profile lists the KLID (a "<LANGID>:<KLID>" value under a tag).
func (s *Setup) inProfile(sid, klid string) bool {
	up := userProfile(sid)
	tags, _ := s.Sys.SubKeys(up)
	for _, t := range tags {
		for _, v := range s.values(up + `\` + t) {
			if strings.HasSuffix(strings.ToLower(v.Name), ":"+strings.ToLower(klid)) {
				return true
			}
		}
	}
	return false
}
