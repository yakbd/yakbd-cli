package policy

import (
	"encoding/json"
	"os"
	"sort"
	"testing"

	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
)

// TestKindsMatchSchema checks the kinds table against the enum of onLoss kinds in the newest embedded schema (0.3 holds 0.1 and 0.2).
func TestKindsMatchSchema(t *testing.T) {
	b, err := os.ReadFile("../format/kbljson/schema/0.3/kbl.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Defs struct {
			OnLoss struct {
				Properties struct {
					Kinds struct {
						PropertyNames struct {
							Enum []string `json:"enum"`
						} `json:"propertyNames"`
					} `json:"kinds"`
				} `json:"properties"`
			} `json:"OnLoss"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	var schemaKinds, ours []string
	for _, k := range s.Defs.OnLoss.Properties.Kinds.PropertyNames.Enum {
		if k[len(k)-1] != '*' && !Known(k) {
			t.Errorf("schema kind %s missing from the table", k)
		}
		if k[len(k)-1] != '*' {
			schemaKinds = append(schemaKinds, k)
		}
	}
	for k := range kindDefaults {
		ours = append(ours, k)
	}
	sort.Strings(ours)
	in := map[string]bool{}
	for _, k := range schemaKinds {
		in[k] = true
	}
	for _, k := range ours {
		if !in[k] {
			t.Errorf("table kind %s not in the schema", k)
		}
	}
}

func TestResolve(t *testing.T) {
	l := &model.Layout{
		OnLoss: &model.OnLoss{Default: "warning", Kinds: map[string]string{"windows.altOnlyLayer": "error", "windows.*": "accept"}},
		Targets: &model.Targets{Windows: &model.WindowsTarget{OnLoss: &model.OnLoss{
			Kinds: map[string]string{"windows.altOnlyLayer": "accept"}}}},
	}
	r := &loss.Report{}
	for _, k := range []string{"windows.altOnlyLayer", "windows.vkMissing", "general.layerOverlap", "general.noLayers", "windowsDefault.vk"} {
		r.Append(loss.Entry{Class: loss.Loss, Code: k})
	}
	r.Append(loss.Entry{Class: loss.Diagnostic, Code: "klc.vk.unknown", Severity: loss.Error})
	Resolve(r, l, "windows", nil)
	want := []loss.Severity{loss.Accepted, loss.Accepted, loss.Error, loss.Warning, loss.Warning, loss.Error}
	for i, e := range r.Entries {
		if e.Severity != want[i] {
			t.Errorf("%s: %v, want %v", e.Code, e.Severity, want[i])
		}
	}
	r2 := &loss.Report{}
	r2.Append(loss.Entry{Class: loss.Defaulted, Code: "windowsDefault.vk"})
	Resolve(r2, &model.Layout{}, "windows", nil)
	if r2.Entries[0].Severity != loss.Accepted {
		t.Error("defaulted entries are accepted by default")
	}
	Resolve(r2, &model.Layout{}, "windows", StrictDefaults)
	if r2.Entries[0].Severity != loss.Error {
		t.Error("strict defaults")
	}
}
