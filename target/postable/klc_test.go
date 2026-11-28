package postable_test

import (
	"os"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/convert/klckbl"
	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	macos "github.com/yakbd/yakbd-cli/target/macos/build"
	xkb "github.com/yakbd/yakbd-cli/target/xkb/build"
)

// imported is testdata/klc/msklc-style.klc (UTF-16 with CRLF, as MSKLC saves it) converted to the model.
func imported(t *testing.T) *model.Layout {
	t.Helper()
	b, err := os.ReadFile("../../testdata/klc/msklc-style.klc")
	if err != nil {
		t.Fatal(err)
	}
	f, r := klc.Parse(b, "msklc-style.klc")
	if f == nil || r.HasErrors() {
		t.Fatal(r.Summary())
	}
	l, r := klckbl.FromKLC(f, "msklc-style.klc")
	if l == nil || r.HasErrors() {
		t.Fatal(r.Summary())
	}
	return l
}

// builds runs both builds at their default policy and returns each report.
func builds(t *testing.T, l *model.Layout) map[string]*loss.Report {
	_, rm := macos.Build(l, macos.Options{})
	_, rx := xkb.Build(l, xkb.Options{})
	return map[string]*loss.Report{"macos": rm, "xkb": rx}
}

// TestKLCBreak: a layout converted from an MSKLC-style .klc file binds Break (MSKLC's implied row), which neither
// the macOS nor the XKB table has. Both builds succeed at their default policy and report the position once as
// <target>.positionWithoutKey (D145).
func TestKLCBreak(t *testing.T) {
	l := imported(t)
	if l.Hardware.Layers[0].Bindings["Break"] == nil {
		t.Fatal("the import has no Break binding: the fixture no longer tests D145")
	}
	for target, r := range builds(t, l) {
		if r.HasErrors() {
			t.Errorf("%s: %s", target, r.Summary())
		}
		var got []string
		for _, e := range r.Entries {
			if e.Code == target+".positionWithoutKey" {
				got = append(got, e.Where.Path+" "+e.Severity.String())
			}
		}
		if strings.Join(got, ",") != "/positions/Break warning" {
			t.Errorf("%s: positionWithoutKey entries %v", target, got)
		}
	}
}

// TestUnknownPositionStillError: a bound position that no table names still needs a pin.
func TestUnknownPositionStillError(t *testing.T) {
	l := imported(t)
	l.Positions["MyKey"] = &model.Position{}
	l.Hardware.Layers[0].Bindings["MyKey"] = l.Hardware.Layers[0].Bindings["KeyQ"]
	for target, r := range builds(t, l) {
		found := false
		for _, e := range r.Entries {
			if e.Where.Path == "/positions/MyKey" {
				found = true
				if e.Severity != loss.Error || !strings.HasSuffix(e.Code, "Missing") {
					t.Errorf("%s: %s", target, e.String())
				}
			}
		}
		if !found {
			t.Errorf("%s: no entry for MyKey", target)
		}
	}
}
