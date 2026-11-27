package main

import (
	"reflect"
	"testing"

	"github.com/yakbd/yakbd-cli/installer/cmdline"
	"github.com/yakbd/yakbd-cli/installer/toolsetup"
)

func TestQuietIn(t *testing.T) {
	for _, a := range [][]string{{"/quiet"}, {"-QN"}, {"/qb!-"}, {"/passive"}, {"/q"}} {
		if !quietIn(a) {
			t.Errorf("%v", a)
		}
	}
	if quietIn([]string{"/x", "/log", "a.log"}) {
		t.Error("not quiet")
	}
}

// TestChildArgs: every request the wizard or a command line makes reaches the elevated copy as the same request
// (without REMOVEUSERDATA, which the first process keeps), whatever action the first command line had.
func TestChildArgs(t *testing.T) {
	on, off := true, false
	reqs := []toolsetup.Request{
		{}, {Uninstall: true}, {Repair: true},
		{AddLocal: []string{}, Remove: []string{toolsetup.Editor}, AddToPath: &on}, // the uninstall wizard: editor only
		{AddLocal: []string{toolsetup.CLI, toolsetup.Editor}, AddToPath: &off},
		{AddLocal: []string{toolsetup.CLI}, Remove: []string{toolsetup.Editor}},
		{Uninstall: true, RemoveUserData: true},
	}
	for _, first := range [][]string{nil, {"/x"}, {"/i", "{F257BACA-1D4A-509F-9245-821ACEAAA3CC}"}} {
		c, err := cmdline.Parse(first)
		if err != nil {
			t.Fatal(err)
		}
		for _, req := range reqs {
			args := childArgs(c, req, `C:\t\r.txt`)
			rest, got, perr := toolsetup.TakeProps(args)
			if perr != nil {
				t.Fatalf("%q: %v", args, perr)
			}
			cc, err := cmdline.Parse(rest)
			if err != nil {
				t.Fatalf("%q: %v", rest, err)
			}
			got.Uninstall = got.Uninstall || cc.Action == cmdline.Uninstall
			got.Repair = cc.Action == cmdline.Repair
			want := req
			want.RemoveUserData = false
			if !cc.Elevated || !reflect.DeepEqual(got, want) {
				t.Errorf("first %q, request %+v: child %q gives %+v", first, req, args, got)
			}
		}
	}
}
