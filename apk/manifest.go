package apk

import (
	"errors"
	"fmt"
	"strings"
)

// androidAttrs are the resource ids of the platform attributes the targets write, from AOSP's
// core/res/res/values/public-final.xml (Apache-2.0). The ids are fixed forever once public.
var androidAttrs = map[string]uint32{
	"theme":                              0x01010000,
	"label":                              0x01010001,
	"icon":                               0x01010002,
	"name":                               0x01010003,
	"permission":                         0x01010006,
	"protectionLevel":                    0x01010009,
	"hasCode":                            0x0101000c,
	"enabled":                            0x0101000e,
	"debuggable":                         0x0101000f,
	"exported":                           0x01010010,
	"process":                            0x01010011,
	"authorities":                        0x01010018,
	"value":                              0x01010024,
	"resource":                           0x01010025,
	"minSdkVersion":                      0x0101020c,
	"versionCode":                        0x0101021b,
	"versionName":                        0x0101021c,
	"settingsActivity":                   0x01010225,
	"targetSdkVersion":                   0x01010270,
	"allowBackup":                        0x01010280,
	"imeSubtypeLocale":                   0x010102ec,
	"imeSubtypeMode":                     0x010102ed,
	"isAuxiliary":                        0x0101037f,
	"keyboardLayout":                     0x010103ab,
	"subtypeId":                          0x010103c1,
	"isAsciiCapable":                     0x010103e9,
	"supportsSwitchingToNextInputMethod": 0x010103eb,
	"extractNativeLibs":                  0x010104ea,
	"directBootAware":                    0x01010505,
	"languageTag":                        0x01010508,
	"compileSdkVersion":                  0x01010572,
	"compileSdkVersionCodename":          0x01010573,
	"keyboardLocale":                     0x0101067e,
	"keyboardLayoutType":                 0x0101067f,
}

// AndroidAttr returns the resource id of the platform attribute android:name (0 when this package does not know
// it).
func AndroidAttr(name string) uint32 { return androidAttrs[name] }

// A returns the attribute android:name with value v; it panics on an attribute AndroidAttr does not know (a
// programming error: add it to the table).
func A(name string, v Value) *Attr {
	id := androidAttrs[name]
	if id == 0 {
		panic("apk: unknown android attribute " + name)
	}
	return &Attr{NS: AndroidNS, Name: name, ResID: id, Value: v}
}

// E returns an element with attributes and children.
func E(name string, attrs []*Attr, children ...*Node) *Node {
	return &Node{Name: name, Attrs: attrs, Children: children}
}

// ManifestPackage returns manifest@package of a parsed AndroidManifest.xml.
func ManifestPackage(root *Node) (string, error) {
	if root == nil || root.Name != "manifest" {
		return "", errors.New("apk: AndroidManifest.xml: the root is not <manifest>")
	}
	a := root.Attr("", "package")
	if a == nil || a.Value.Type != TypeString || a.Value.Str == "" {
		return "", errors.New("apk: AndroidManifest.xml: no package")
	}
	return a.Value.Str, nil
}

// classAttrs are the attributes that name a class, per element; a relative name (".Foo", or "Foo" without a dot)
// is resolved against manifest@package, so RenamePackage writes them in full first.
var classAttrs = map[string][]string{
	"application":     {"name", "backupAgent", "manageSpaceActivity", "appComponentFactory"},
	"activity":        {"name", "parentActivityName"},
	"activity-alias":  {"name", "targetActivity", "parentActivityName"},
	"service":         {"name"},
	"receiver":        {"name"},
	"provider":        {"name"},
	"instrumentation": {"name"},
}

// renamedNames are the attributes whose value is a name the package owns and that has the package name as its
// prefix by convention: permissions (AndroidX declares <pkg>.DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION, and two
// packages may not declare the same permission), permission groups and trees, the permissions components require,
// and the task affinity and process names.
var renamedNames = map[string]bool{
	"permission": true, "readPermission": true, "writePermission": true, "permissionGroup": true,
	"taskAffinity": true, "process": true,
}

// RenamePackage changes the package of a parsed AndroidManifest.xml from its current name to pkg: manifest@package,
// every name of the form <old> or <old>.<rest> in permission names (<permission>, <permission-group>,
// <permission-tree>, <uses-permission>), in the permissions, task affinities and process names of the manifest's
// elements, and in provider authorities (each of a ';' list). Class names are never renamed (the code does not
// move): a relative class name is first written in full with the old package. It returns the old name.
func RenamePackage(root *Node, pkg string) (string, error) {
	old, err := ManifestPackage(root)
	if err != nil {
		return "", err
	}
	if !validPackage(pkg) {
		return "", fmt.Errorf("apk: %q is not a valid package name (two or more parts of letters, digits and '_', each starting with a letter)", pkg)
	}
	ren := func(s string) string {
		if s == old {
			return pkg
		}
		if strings.HasPrefix(s, old+".") {
			return pkg + s[len(old):]
		}
		return s
	}
	root.Walk(func(n *Node) {
		for _, name := range classAttrs[n.Name] {
			a := n.Attr(AndroidNS, name)
			if a == nil || a.Value.Type != TypeString {
				continue
			}
			if v := a.Value.Str; strings.HasPrefix(v, ".") {
				a.Value.Str = old + v
			} else if v != "" && !strings.Contains(v, ".") {
				a.Value.Str = old + "." + v
			}
		}
		for _, a := range n.Attrs {
			if a.NS != AndroidNS || a.Value.Type != TypeString {
				continue
			}
			switch {
			case a.Name == "name" && (n.Name == "permission" || n.Name == "permission-group" || n.Name == "permission-tree" ||
				n.Name == "uses-permission" || n.Name == "uses-permission-sdk-23"):
				a.Value.Str = ren(a.Value.Str)
			case renamedNames[a.Name]:
				a.Value.Str = ren(a.Value.Str)
			case a.Name == "authorities":
				parts := strings.Split(a.Value.Str, ";")
				for i := range parts {
					parts[i] = ren(parts[i])
				}
				a.Value.Str = strings.Join(parts, ";")
			}
		}
	})
	root.Attr("", "package").Value.Str = pkg
	return old, nil
}

// validPackage reports whether s is a valid Android package name.
func validPackage(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(s) > 255 {
		return false
	}
	for _, p := range parts {
		if p == "" || !(p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z') {
			return false
		}
		for _, c := range p {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
				return false
			}
		}
	}
	return true
}

// ValidPackage reports whether s is a valid Android package name: two or more parts separated by '.', each of
// ASCII letters, digits and '_' starting with a letter.
func ValidPackage(s string) bool { return validPackage(s) }

// SetLabel sets the label of <application> to the literal text label (a reference to a string resource is
// replaced). Elements below it without a label of their own show this one.
func SetLabel(root *Node, label string) error {
	if root == nil || root.Name != "manifest" {
		return errors.New("apk: AndroidManifest.xml: the root is not <manifest>")
	}
	apps := root.Elements("application")
	if len(apps) != 1 {
		return errors.New("apk: AndroidManifest.xml: not exactly one <application>")
	}
	apps[0].SetAttr(AndroidNS, "label", AndroidAttr("label"), String(label))
	return nil
}
