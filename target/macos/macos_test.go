package macos

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/format/keylayout"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/macos/build"
)

const demo = "../../testdata/kbl-spec/examples/macos-option-deadkeys.kbl.json"

func demoBuild(t *testing.T) (*model.Layout, *build.Result) {
	t.Helper()
	data, err := os.ReadFile(demo)
	if err != nil {
		t.Fatal(err)
	}
	l, r := kbljson.Read(data, demo)
	if r.HasErrors() {
		t.Fatal(r.Summary())
	}
	res, br := build.Build(l, build.Options{})
	if res == nil {
		t.Fatal(br.Summary())
	}
	return l, res
}

func TestBundle(t *testing.T) {
	_, res := demoBuild(t)
	files := Bundle(res)
	name := res.Pins.BundleName
	want := []string{name + ".bundle/Contents/Info.plist", name + ".bundle/Contents/Resources/" + res.Pins.LayoutName + ".keylayout"}
	for i, w := range want {
		if files[i].Path != w {
			t.Errorf("file %d: %s, want %s", i, files[i].Path, w)
		}
	}
	plist := string(files[0].Data)
	for _, s := range []string{"<key>CFBundleIdentifier</key>\n\t<string>" + res.Pins.BundleID + "</string>",
		"<key>KLInfo_" + res.Pins.LayoutName + "</key>", "<key>TISInputSourceID</key>\n\t\t<string>" + res.Pins.InputSourceID + "</string>",
		"<key>TISIntendedLanguage</key>"} {
		if !strings.Contains(plist, s) {
			t.Errorf("Info.plist lacks %q:\n%s", s, plist)
		}
	}
	if k, r := keylayout.Read(files[1].Data, "x"); k == nil || k.Name != res.Pins.LayoutName {
		t.Errorf("the .keylayout: %s", r.Summary())
	}
}

func TestZip(t *testing.T) {
	_, res := demoBuild(t)
	a, err := Zip(res)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Zip(res)
	if !bytes.Equal(a, b) {
		t.Error("the zip is not deterministic")
	}
	zr, err := zip.NewReader(bytes.NewReader(a), int64(len(a)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
		if f.Name == "README.txt" {
			rc, _ := f.Open()
			text, _ := io.ReadAll(rc)
			if !strings.Contains(string(text), "~/Library/Keyboard Layouts") || !strings.Contains(string(text), "not signed") {
				t.Errorf("README:\n%s", text)
			}
		}
	}
	for _, f := range Bundle(res) {
		if !names[f.Path] {
			t.Errorf("zip lacks %s", f.Path)
		}
	}
	if !names[res.Pins.BundleName+".bundle/"] || !names["README.txt"] {
		t.Errorf("zip entries %v", names)
	}
}

func TestExpectations(t *testing.T) {
	l, res := demoBuild(t)
	es := Expectations(l, res, 0)
	got := map[string]string{}
	for _, e := range es {
		got[e.Name] = e.Want
	}
	// from the example: KeyQ types ng, Option+E is the dead acute, acute then circumflex chains
	for name, want := range map[string]string{"KeyQ": "ng", "KeyQ+shift": "NG", "dead-acute-KeyE": "é",
		"chain-acute-acuteCircumflex-KeyE": "ế", "dead-acute-twice": "´´"} {
		if got[name] != want {
			t.Errorf("%s: %q, want %q", name, got[name], want)
		}
	}
	if n := len(Expectations(l, res, 20)); n != 20 {
		t.Errorf("max 20: %d lines", n)
	}
	tsv := string(FormatExpectations(es))
	if !strings.Contains(tsv, "dead-acute-twice\t14+option 14+option\t´´\n") {
		t.Errorf("expect.tsv:\n%s", tsv)
	}
}
