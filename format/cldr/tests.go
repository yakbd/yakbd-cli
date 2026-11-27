package cldr

import (
	"strconv"
	"strings"

	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
)

// TestsConformsTo is the conformsTo value of CLDR keyboard test files (technical preview).
const TestsConformsTo = "techpreview"

// ReadTests parses a CLDR keyboard test file (keyboardTest3) into a TestFile for Layout.Tests.
func ReadTests(data []byte, name string) ([]*model.TestFile, *loss.Report) {
	rd := &reader{r: &loss.Report{}, name: name}
	root, err := parseTree(data)
	if err != nil {
		rd.r.Errorf("cldr.xml.syntax", loss.Where{File: name}, "%v", err)
		return nil, rd.r
	}
	if root.name != "keyboardTest3" {
		rd.diag(root, "root", "the root element is <%s>, not <keyboardTest3>", root.name)
		return nil, rd.r
	}
	tf := &model.TestFile{CLDRSpecial: root.specials()}
	text := func(n *node, attr string) string {
		v, err := decodeEscapes(n.get(attr))
		if err != nil {
			rd.diag(n, "escape", "%v", err)
		}
		return v
	}
	for _, c := range root.children {
		switch c.name {
		case "info":
			tf.Info = model.TestInfo{Name: c.get("name"), Author: c.get("author"), Keyboard: c.get("keyboard")}
		case "repertoire":
			tf.Repertoires = append(tf.Repertoires, model.Repertoire{Name: c.get("name"), Chars: c.get("chars"), Type: c.get("type")})
		case "tests":
			s := &model.Suite{Name: c.get("name"), CLDRSpecial: c.specials()}
			for _, t := range c.kids("test") {
				mt := &model.Test{Name: t.get("name"), CLDRSpecial: t.specials(), Steps: []model.Step{}}
				for _, st := range t.children {
					switch st.name {
					case "startContext":
						v := text(st, "to")
						mt.StartContext = &v
					case "keystroke":
						ks := &model.Keystroke{Key: st.get("key"), Flick: strings.Fields(st.get("flick"))}
						if v, ok := st.attr("longPress"); ok {
							n, err := strconv.Atoi(v)
							if err != nil {
								rd.diag(st, "longPress", "longPress %q", v)
							}
							ks.LongPress = &n
						}
						if v, ok := st.attr("tapCount"); ok {
							n, err := strconv.Atoi(v)
							if err != nil {
								rd.diag(st, "tapCount", "tapCount %q", v)
							}
							ks.TapCount = n
						}
						mt.Steps = append(mt.Steps, model.Step{Keystroke: ks})
					case "emit":
						v := text(st, "to")
						mt.Steps = append(mt.Steps, model.Step{Emit: &v})
					case "backspace":
						mt.Steps = append(mt.Steps, model.Step{Backspace: true})
					case "check":
						v := text(st, "result")
						mt.Steps = append(mt.Steps, model.Step{Check: &v})
					}
				}
				s.Tests = append(s.Tests, mt)
			}
			tf.Suites = append(tf.Suites, s)
		}
	}
	if rd.r.HasErrors() {
		return nil, rd.r
	}
	return []*model.TestFile{tf}, rd.r
}

// WriteTests renders one TestFile as a CLDR keyboard test file. Hardware keystrokes (by position) have no CLDR
// form: those tests are left out and reported as general.testsDropped.
func WriteTests(tf *model.TestFile) ([]byte, *loss.Report) {
	r := &loss.Report{}
	var w xw
	w.line(`<?xml version="1.0" encoding="UTF-8"?>`)
	w.tag("keyboardTest3", []kv{{"conformsTo", TestsConformsTo}}, false)
	w.tag("info", []kv{{"keyboard", tf.Info.Keyboard}, {"author", tf.Info.Author}, {"name", tf.Info.Name}}, true)
	for _, rp := range tf.Repertoires {
		w.tag("repertoire", []kv{{"name", rp.Name}, {"chars", rp.Chars}, {"type", rp.Type}}, true)
	}
	for _, s := range tf.Suites {
		w.tag("tests", []kv{{"name", s.Name}}, false)
		for _, t := range s.Tests {
			hardware := false
			for _, st := range t.Steps {
				if st.Keystroke != nil && st.Keystroke.Position != "" {
					hardware = true
				}
			}
			if hardware {
				lost(r, "general.testsDropped", loss.Where{Path: "/tests"}, map[string]any{"test": t.Name},
					"test %s presses positions, which CLDR test files cannot express", t.Name)
				continue
			}
			w.tag("test", []kv{{"name", t.Name}}, false)
			if t.StartContext != nil {
				w.tagKeepEmpty("startContext", []kv{{"to", encodeText(*t.StartContext)}}, true)
			}
			for _, st := range t.Steps {
				switch {
				case st.Keystroke != nil:
					ks := st.Keystroke
					a := []kv{{"key", ks.Key}, {"flick", strings.Join(ks.Flick, " ")}}
					if ks.LongPress != nil {
						a = append(a, kv{"longPress", strconv.Itoa(*ks.LongPress)})
					}
					if ks.TapCount != 0 {
						a = append(a, kv{"tapCount", strconv.Itoa(ks.TapCount)})
					}
					w.tag("keystroke", a, true)
				case st.Emit != nil:
					w.tagKeepEmpty("emit", []kv{{"to", encodeText(*st.Emit)}}, true)
				case st.Backspace:
					w.tag("backspace", nil, true)
				case st.Check != nil:
					w.tagKeepEmpty("check", []kv{{"result", encodeText(*st.Check)}}, true)
				}
			}
			w.raw(t.CLDRSpecial)
			w.end("test")
		}
		w.raw(s.CLDRSpecial)
		w.end("tests")
	}
	w.raw(tf.CLDRSpecial)
	w.end("keyboardTest3")
	return []byte(w.b.String()), r
}
