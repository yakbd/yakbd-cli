package apk

import (
	"errors"
	"fmt"
	"strings"
)

// Changes is what Repackage changes in a prebuilt APK.
type Changes struct {
	// Package is the new package name ("" keeps it): RenamePackage.
	Package string
	// Label is the new application label ("" keeps it): SetLabel.
	Label string
	// Files are entries to replace or add, by archive name ("assets/layout.json"), compressed when new.
	Files map[string][]byte
}

// Repackage rewrites a prebuilt APK (a runtime): its manifest per c, the files of c, every earlier signature
// dropped, signed again by s. It returns the new APK and the package name it had. resources.arsc keeps its
// package name: Android takes an application's package from the manifest.
func Repackage(apkData []byte, c Changes, s *Signer) ([]byte, string, error) {
	a, err := Read(apkData)
	if err != nil {
		return nil, "", err
	}
	me := a.Get("AndroidManifest.xml")
	if me == nil {
		return nil, "", errors.New("apk: no AndroidManifest.xml")
	}
	root, err := ParseXML(me.Data)
	if err != nil {
		return nil, "", fmt.Errorf("AndroidManifest.xml: %v", err)
	}
	old, err := ManifestPackage(root)
	if err != nil {
		return nil, "", err
	}
	if c.Package != "" {
		if _, err := RenamePackage(root, c.Package); err != nil {
			return nil, "", err
		}
	}
	if c.Label != "" {
		if err := SetLabel(root, c.Label); err != nil {
			return nil, "", err
		}
	}
	if me.Data, err = EncodeXML(root); err != nil {
		return nil, "", err
	}
	for _, name := range sortedKeys(c.Files) {
		if name == "AndroidManifest.xml" || IsSignatureFile(name) || strings.HasPrefix(name, "/") {
			return nil, "", fmt.Errorf("apk: cannot replace %s", name)
		}
		a.Put(name, c.Files[name], false)
	}
	out, err := a.Sign(s)
	return out, old, err
}
