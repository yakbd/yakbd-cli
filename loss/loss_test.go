package loss

import (
	"strings"
	"testing"
)

func TestWhereString(t *testing.T) {
	tests := []struct {
		in   Where
		want string
	}{
		{Where{}, ""},
		{Where{File: "a.klc"}, "a.klc"},
		{Where{File: "a.klc", Line: 3}, "a.klc:3"},
		{Where{Line: 3}, "line 3"},
		{Where{File: "a.klc", Line: 3, Path: "LAYOUT"}, "a.klc:3: LAYOUT"},
		{Where{Path: "keys[1]"}, "keys[1]"},
	}
	for _, tt := range tests {
		if got := tt.in.String(); got != tt.want {
			t.Errorf("%+v: got %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestReportCountsAndPolicy(t *testing.T) {
	var r Report
	r.Warnf("x.drop", Where{Line: 1}, "dropped %d", 1)
	r.Errorf("x.bad", Where{}, "bad")
	r.Infof("x.note", Where{}, "note")
	if !r.HasErrors() || r.Count(Warning) != 1 || r.Count(Info) != 1 {
		t.Fatalf("counts wrong: %+v", r)
	}
	if got := strings.Join(r.Codes(), ","); got != "x.bad,x.drop,x.note" {
		t.Errorf("codes %q", got)
	}
	r.Apply(map[string]Severity{"x.bad": Accepted, "x.drop": Error})
	if r.Count(Accepted) != 1 || r.Count(Error) != 1 {
		t.Errorf("policy not applied: %+v", r.Entries)
	}
	if r.Err() == nil {
		t.Error("Err() = nil with an error entry")
	}
	s := r.Summary()
	if !strings.HasPrefix(s, "1 error(s), 0 warning(s), 1 accepted, 1 info; 0 loss(es), 0 default(s) used\n") {
		t.Errorf("summary %q", s)
	}
	if !strings.Contains(strings.SplitN(s, "\n", 3)[1], "x.drop") {
		t.Errorf("errors not first: %q", s)
	}
}

func TestEmptyReport(t *testing.T) {
	var r Report
	if r.HasErrors() || r.Err() != nil {
		t.Error("empty report has errors")
	}
	r.Merge(nil)
	other := &Report{}
	other.Warnf("w", Where{}, "w")
	r.Merge(other)
	if len(r.Entries) != 1 {
		t.Error("merge failed")
	}
}

func TestDefaultedPolicy(t *testing.T) {
	var r Report
	r.Defaulted("windows.vk.default-scancode", Where{Line: 4}, "scan code 10 took its virtual key from the default table")
	r.Lost(Warning, "klc.version.dropped", Where{}, "VERSION text dropped")
	if r.HasErrors() || r.CountClass(Defaulted) != 1 || r.CountClass(Loss) != 1 {
		t.Fatalf("%+v", r.Entries)
	}
	if !strings.Contains(r.Entries[0].String(), "info (defaulted)") {
		t.Errorf("%q", r.Entries[0].String())
	}
	r.ApplyClass(Defaulted, Error)
	if !r.HasErrors() || r.Count(Error) != 1 {
		t.Error("strict-defaults policy not applied")
	}
}
