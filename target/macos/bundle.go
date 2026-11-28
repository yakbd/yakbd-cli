package macos

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/yakbd/yakbd-cli/format/keylayout"
	"github.com/yakbd/yakbd-cli/target/macos/build"
)

// BundleFile is one file of a layout bundle, its path relative to the folder that holds the bundle.
type BundleFile struct {
	Path string
	Data []byte
}

// Bundle returns the files of the layout bundle of a build (targets/macos.md §5): Info.plist, the .keylayout, the
// .icns when there is an icon, and one InfoPlist.strings per localised name.
func Bundle(res *build.Result) []BundleFile {
	p := res.Pins
	root := p.BundleName + ".bundle/Contents/"
	files := []BundleFile{{Path: root + "Info.plist", Data: infoPlist(res)},
		{Path: root + "Resources/" + p.LayoutName + ".keylayout", Data: keylayout.Write(res.Keyboard)}}
	if res.Icon != nil {
		files = append(files, BundleFile{Path: root + "Resources/" + p.LayoutName + ".icns", Data: res.Icon})
	}
	var tags []string
	for t := range p.LocalizedNames {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	for _, t := range tags {
		line := fmt.Sprintf("\"%s\" = \"%s\";\n", stringsEscape(p.LayoutName), stringsEscape(p.LocalizedNames[t]))
		files = append(files, BundleFile{Path: root + "Resources/" + t + ".lproj/InfoPlist.strings", Data: []byte(line)})
	}
	return files
}

func stringsEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

func plistEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func infoPlist(res *build.Result) []byte {
	p := res.Pins
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`)
	kv := func(indent, k, v string) {
		fmt.Fprintf(&b, "%s<key>%s</key>\n%s<string>%s</string>\n", indent, plistEscape(k), indent, plistEscape(v))
	}
	kv("\t", "CFBundleIdentifier", p.BundleID)
	kv("\t", "CFBundleName", p.BundleName)
	kv("\t", "CFBundleShortVersionString", p.BundleVersion)
	kv("\t", "CFBundleVersion", p.BundleVersion)
	fmt.Fprintf(&b, "\t<key>KLInfo_%s</key>\n\t<dict>\n", plistEscape(p.LayoutName))
	if res.Icon != nil {
		t := p.Icon.Template != nil && *p.Icon.Template
		fmt.Fprintf(&b, "\t\t<key>TISIconIsTemplate</key>\n\t\t<%t/>\n", t)
	}
	kv("\t\t", "TISInputSourceID", p.InputSourceID)
	kv("\t\t", "TISIntendedLanguage", p.Language)
	b.WriteString("\t</dict>\n</dict>\n</plist>\n")
	return b.Bytes()
}

// Readme is the README of the release zip (targets/macos.md §5, D126).
func Readme(res *build.Result) []byte {
	p := res.Pins
	return []byte(fmt.Sprintf(`%[1]s: a keyboard layout for macOS
%[2]s

This zip holds %[3]s.bundle, a keyboard layout bundle made by yakbd (https://github.com/yakbd).

Install
1. Copy %[3]s.bundle into the folder Keyboard Layouts of your Library:
   - for you only: ~/Library/Keyboard Layouts (in Finder: Go > Go to Folder, type ~/Library/Keyboard Layouts);
   - for every user of this Mac: /Library/Keyboard Layouts (an administrator's password is asked for).
2. Open System Settings > Keyboard > Text Input > Edit, click +, find "%[1]s" (under its language or under
   Others) and add it. If it is not listed yet, sign out and sign in again, then add it.
3. Select it in the input menu in the menu bar, or with Control-Space.

Remove
Remove the layout in System Settings > Keyboard > Text Input > Edit, then delete %[3]s.bundle from the folder.

The bundle is not signed. It holds no program code, and macOS uses it without an "Open Anyway" step.

Input source id: %[4]s
`, p.LayoutName, strings.Repeat("=", len([]rune(p.LayoutName))+len(": a keyboard layout for macOS")), p.BundleName, p.InputSourceID))
}

// zipTime is the fixed modification time of every zip entry, so that the same layout gives the same zip.
var zipTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Zip returns the release zip: the bundle folder at the root and README.txt.
func Zip(res *build.Result) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	dirs := map[string]bool{}
	var add func(name string, data []byte, dir bool) error
	add = func(name string, data []byte, dir bool) error {
		if parent := path.Dir(strings.TrimSuffix(name, "/")); parent != "." && !dirs[parent+"/"] {
			dirs[parent+"/"] = true
			if err := add(parent+"/", nil, true); err != nil {
				return err
			}
		}
		h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipTime}
		mode := fs.FileMode(0o644)
		if dir {
			h.Method = zip.Store
			mode = fs.ModeDir | 0o755
		}
		h.SetMode(mode)
		f, err := w.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	}
	for _, f := range Bundle(res) {
		if err := add(f.Path, f.Data, false); err != nil {
			return nil, err
		}
	}
	if err := add("README.txt", Readme(res), false); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
