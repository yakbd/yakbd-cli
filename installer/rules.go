package installer

import (
	"crypto/sha1"
	"fmt"
	"strconv"
	"strings"

	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
)

// productNamespace is the UUID namespace of rule productCode (kbl targets/windows-install.md section 2, D48).
var productNamespace = [16]byte{0x52, 0x71, 0x1d, 0x5d, 0x5d, 0xcb, 0x4d, 0x0b, 0xb8, 0xbb, 0x98, 0x52, 0xab, 0xd8, 0x99, 0xea}

// DefaultProductCode is rule productCode (D48 as amended by D61): the name-based UUID (version 5, SHA-1, RFC 9562)
// of productNamespace and the lower-case metadata.id, one space and the lower-case dllName (without ".dll"), upper
// case in braces.
func DefaultProductCode(id, dllName string) string {
	name := strings.ToLower(id) + " " + strings.TrimSuffix(strings.ToLower(dllName), ".dll")
	return "{" + strings.ToUpper(uuid5(productNamespace, name)) + "}"
}

// uuid5 is a name-based UUID, version 5 (RFC 9562 section 5.5), in its lower-case text form.
func uuid5(ns [16]byte, name string) string {
	h := sha1.New()
	h.Write(ns[:])
	h.Write([]byte(name))
	b := h.Sum(nil)[:16]
	b[6] = b[6]&0x0F | 0x50
	b[8] = b[8]&0x3F | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Decisions are the values of the installation rules for one layout (targets/windows-install.md section 1).
type Decisions struct {
	ProductCode string
	Enable      bool
	// SignIn is rule enableForSignInAndNewUsers (section 7.1, D87): the sign-in screen and the Default profile.
	SignIn         bool
	ProductName    string
	ProductVersion string
	Publisher      string
}

// Decide applies the rules productCode, enableForUser, enableForSignInAndNewUsers and uninstallEntry, reporting every use as
// windowsDefault.<rule>, and the checks of section 2 (windows.registrationInvalid). pins is the lowered Windows
// target (build.Result.Pins: dllName, versionInfo and registration decided); the file's install object comes from l.
func Decide(l *model.Layout, pins *model.WindowsTarget) (Decisions, *loss.Report) {
	r := &loss.Report{}
	var d Decisions
	reg := &model.Registration{}
	if pins != nil && pins.Registration != nil {
		reg = pins.Registration
	}
	inst := &model.Install{}
	if t := l.Targets; t != nil && t.Windows != nil && t.Windows.Install != nil {
		inst = t.Windows.Install
	}
	dll := ""
	if pins != nil {
		dll = pins.DLLName
	}
	defaulted := func(rule string, value any, msg string) {
		r.Append(loss.Entry{Class: loss.Defaulted, Severity: loss.Accepted, Code: "windowsDefault." + rule,
			Where: loss.Where{Path: "/targets/windows"}, Message: msg, Detail: map[string]any{"value": value}})
	}

	d.ProductCode = strings.ToUpper(reg.ProductCode)
	if d.ProductCode == "" {
		d.ProductCode = DefaultProductCode(l.Metadata.ID, dll)
		defaulted("productCode", d.ProductCode, "product code "+d.ProductCode+" by rule (from metadata.id and the DLL name)")
	}
	if inst.EnableForUser != nil {
		d.Enable = *inst.EnableForUser
	} else {
		d.Enable = true
		defaulted("enableForUser", true, "the installer enables the layout for the installing user by rule")
	}
	if inst.EnableForSignInAndNewUsers != nil {
		d.SignIn = *inst.EnableForSignInAndNewUsers
	} else {
		defaulted("enableForSignInAndNewUsers", false, "the installer leaves the sign-in screen and new accounts alone by rule")
	}
	entry := map[string]any{}
	d.ProductName = inst.ProductName
	if d.ProductName == "" {
		d.ProductName = l.Metadata.Name
		entry["productName"] = d.ProductName
	}
	d.ProductVersion = inst.ProductVersion
	if d.ProductVersion == "" {
		if pins != nil && pins.VersionInfo != nil {
			d.ProductVersion = pins.VersionInfo.ProductVersion
		}
		entry["productVersion"] = d.ProductVersion
	}
	d.Publisher = inst.Publisher
	if d.Publisher == "" && l.Metadata.Organization != "" {
		d.Publisher = l.Metadata.Organization
		entry["publisher"] = d.Publisher
	}
	if len(entry) > 0 {
		defaulted("uninstallEntry", entry, "uninstall entry name, version or publisher by rule")
	}

	bad := func(path, format string, a ...any) {
		r.Lost(loss.Error, "windows.registrationInvalid", loss.Where{Path: path}, format, a...)
	}
	lang, _ := strconv.ParseUint(reg.LangID, 16, 16)
	if reg.KLID != "" && !strings.EqualFold(reg.KLID[4:], reg.LangID) {
		bad("/targets/windows/registration/klid", "KLID %s is not of LANGID %s", reg.KLID, reg.LangID)
	}
	if reg.LayoutID != "" {
		if id, _ := strconv.ParseUint(reg.LayoutID, 16, 16); id == 0 || id > 0xFFF {
			bad("/targets/windows/registration/layoutId", "Layout Id %s is outside 0001-0FFF (inst section 3.3)", reg.LayoutID)
		}
	}
	if d.SignIn && lang == 0x0C00 {
		bad("/targets/windows/install/enableForSignInAndNewUsers", "a LANGID 0C00 layout cannot be put on the sign-in screen or into new accounts: its transient LANGID is per user (targets/windows-install.md section 7.1)")
	}
	if lang == 0x1000 || lang == 0x1400 || lang >= 0x2000 && lang <= 0x4C00 && lang%0x400 == 0 {
		bad("/targets/windows/registration/langId", "LANGID %s is an unspecified or per-user LANGID (inst section 6)", reg.LangID)
	}
	return d, r
}

// Pin writes the results of the installation rules into an already pinned layout (registration.productCode,
// install.*), so that the file no longer depends on them (targets/windows-install.md section 0 item 3).
func Pin(l *model.Layout) *loss.Report {
	if l.Targets == nil || l.Targets.Windows == nil {
		return &loss.Report{}
	}
	w := l.Targets.Windows
	d, r := Decide(l, w)
	if w.Registration == nil {
		w.Registration = &model.Registration{}
	}
	w.Registration.ProductCode = d.ProductCode
	if w.Install == nil {
		w.Install = &model.Install{}
	}
	e := d.Enable
	w.Install.EnableForUser = &e
	si := d.SignIn
	w.Install.EnableForSignInAndNewUsers = &si
	w.Install.ProductName = d.ProductName
	w.Install.ProductVersion = d.ProductVersion
	w.Install.Publisher = d.Publisher
	return r
}

// ToolProductCode is the product code of yakbd-setup.exe, the tool's own installer (docs/installer-cli.md §12):
// the name-based UUID of productNamespace and "yakbd tool installer", which no layout's "id dll" name gives.
func ToolProductCode() string {
	return "{" + strings.ToUpper(uuid5(productNamespace, "yakbd tool installer")) + "}"
}
