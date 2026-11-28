package keylayout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `<?xml version="1.1" encoding="UTF-8"?>
<!DOCTYPE keyboard SYSTEM "file://localhost/System/Library/DTDs/KeyboardLayout.dtd">
<!-- a comment -->
<keyboard group="126" id="-1234" name="Test &amp; more" maxout="3">
	<layouts>
		<layout first="0" last="17" modifiers="mods" mapSet="ANSI"/>
		<layout first="18" last="18" modifiers="mods" mapSet="JIS"/>
	</layouts>
	<modifierMap id="mods" defaultIndex="0">
		<keyMapSelect mapIndex="0">
			<modifier keys=""/>
			<modifier keys="command anyShift? caps?"/>
		</keyMapSelect>
		<keyMapSelect mapIndex="1">
			<modifier keys="anyShift caps?"/>
		</keyMapSelect>
		<keyMapSelect mapIndex="2">
			<modifier keys="anyOption anyShift? caps?"/>
		</keyMapSelect>
		<keyMapSelect mapIndex="3">
			<modifier keys="caps"/>
		</keyMapSelect>
	</modifierMap>
	<keyMapSet id="ANSI">
		<keyMap index="0">
			<key code="0" output="a"/>
			<key code="1" output="&#x0008;&#x1F600;&#xD83D;&#xDE00;"/>
			<key code="2" action="acute"/>
			<key code="3">
				<action>
					<when state="none" output="e"/>
					<when state="acute" output="é"/>
				</action>
			</key>
		</keyMap>
		<keyMap index="1">
			<key code="0" output="A"/>
		</keyMap>
		<keyMap index="2">
			<key code="0" output="&lt;&quot;&gt;"/>
		</keyMap>
		<keyMap index="3">
			<key code="0" output="A"/>
		</keyMap>
	</keyMapSet>
	<keyMapSet id="JIS">
		<keyMap index="0" baseMapSet="ANSI" baseIndex="0">
			<key code="93" output="¥"/>
		</keyMap>
		<keyMap index="1" baseMapSet="ANSI" baseIndex="1"/>
		<keyMap index="2" baseMapSet="ANSI" baseIndex="2"/>
		<keyMap index="3" baseMapSet="ANSI" baseIndex="3"/>
	</keyMapSet>
	<actions>
		<action id="acute">
			<when state="none" next="acute"/>
			<when state="acute" output="´"/>
		</action>
		<action id="hex">
			<when state="none" next="1"/>
			<when state="1" through="16" multiplier="16" next="17"/>
		</action>
	</actions>
	<terminators>
		<when state="acute" output="´"/>
	</terminators>
</keyboard>
`

func mustRead(t *testing.T, b []byte) *Keyboard {
	t.Helper()
	k, r := Read(b, "t.keylayout")
	if k == nil || r.HasErrors() {
		t.Fatalf("read: %s", r.Summary())
	}
	return k
}

func TestReadSample(t *testing.T) {
	k := mustRead(t, []byte(sample))
	if k.Name != "Test & more" || k.ID != -1234 || k.Group != 126 || k.MaxOut == nil || *k.MaxOut != 3 {
		t.Fatalf("header: %+v", k)
	}
	keys := k.Keys("ANSI", 0)
	if got := *keys[1].Output; got != "\b😀😀" {
		t.Errorf("output with references: %q", got)
	}
	if got := *k.Keys("ANSI", 2)[0].Output; got != `<">` {
		t.Errorf("entities: %q", got)
	}
	jis := k.Keys("JIS", 0)
	if len(jis) != 5 || *jis[93].Output != "¥" || *jis[0].Output != "a" {
		t.Errorf("base map: %v", jis)
	}
	if a := k.KeyAction(keys[3]); a == nil || a.When("acute") == nil || *a.When("acute").Output != "é" {
		t.Errorf("inline action")
	}
	if !k.Action("hex").Whens[1].Range() {
		t.Errorf("range when")
	}
	if s, ok := k.Terminator("acute"); !ok || s != "´" {
		t.Errorf("terminator")
	}
}

func TestWriteStable(t *testing.T) {
	inputs := map[string][]byte{"sample": []byte(sample)}
	files, _ := filepath.Glob("../../tools/macos-check/cases/*/*.bundle/Contents/Resources/*.keylayout")
	more, _ := filepath.Glob("../../testdata/third_party/keylayout/*/*.keylayout")
	for _, f := range append(files, more...) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		inputs[f] = b
	}
	for name, b := range inputs {
		k := mustRead(t, b)
		w1 := Write(k)
		w2 := Write(mustRead(t, w1))
		if string(w1) != string(w2) {
			t.Errorf("%s: write, read, write differs:\n%s\n---\n%s", name, w1, w2)
		}
	}
	w := string(Write(mustRead(t, []byte(sample))))
	for _, want := range []string{`name="Test &#x0026; more"`, `output="&#x0008;😀😀"`, `output="&#x003C;&#x0022;&#x003E;"`,
		"<keyMap index=\"1\" baseMapSet=\"ANSI\" baseIndex=\"1\"/>", `<when state="1" through="16" multiplier="16" next="17"/>`} {
		if !strings.Contains(w, want) {
			t.Errorf("written form lacks %s:\n%s", want, w)
		}
	}
}

func TestModifierMatching(t *testing.T) {
	k := mustRead(t, []byte(sample))
	m := k.ModifierMap("mods")
	cases := []struct {
		s    State
		want int
	}{
		{0, 0}, {Shift, 1}, {RightShift, 1}, {Shift | RightShift, 1}, {Shift | Caps, 1}, {Caps, 3},
		{Option, 2}, {RightOption | Shift, 2}, {Command, 0}, {Command | Shift, 0}, {Control, 0}, {Command | Option, 0},
	}
	for _, c := range cases {
		if got := m.Index(c.s); got != c.want {
			t.Errorf("state %08b: index %d, want %d", c.s, got, c.want)
		}
	}
	words := []struct {
		keys string
		s    State
		want bool
	}{
		{"shift", Shift, true}, {"shift", RightShift, false}, {"shift", Shift | RightShift, false},
		{"rightShift", RightShift, true}, {"anyShift", 0, false}, {"anyShift?", 0, true}, {"anyShift?", Shift, true},
		{"shift?", RightShift, false}, {"anyShift rightShift", Shift, false}, {"anyShift rightShift", RightShift, true},
		{"", Caps, false}, {"caps?", Caps, true}, {"command", Command | Caps, false},
	}
	for _, w := range words {
		if got := (Modifier{Keys: strings.Fields(w.keys)}).Matches(w.s); got != w.want {
			t.Errorf("%q on %08b: %v, want %v", w.keys, w.s, got, w.want)
		}
	}
}

func TestReadErrors(t *testing.T) {
	cases := map[string]string{
		"version":    `<?xml version="2.0"?><keyboard/>`,
		"root":       `<layouts/>`,
		"no layouts": `<keyboard group="126" id="-1" name="x"><modifierMap id="m" defaultIndex="0"/><keyMapSet id="s"/></keyboard>`,
		"bad ref":    strings.Replace(sample, `mapSet="JIS"`, `mapSet="NOPE"`, 1),
		"dup code":   strings.Replace(sample, `<key code="1" `, `<key code="0" `, 1),
		"action ref": strings.Replace(sample, `action="acute"`, `action="grave"`, 1),
		"surrogate":  strings.Replace(sample, `&#xDE00;`, ``, 1),
		"entity":     strings.Replace(sample, `&amp;`, `&nbsp;`, 1),
		"nul":        strings.Replace(sample, `&#x0008;`, `&#x0;`, 1),
		"modifier":   strings.Replace(sample, `anyShift caps?`, `anyShift fn`, 1),
		"two forms":  strings.Replace(sample, `<key code="2" action="acute"/>`, `<key code="2" action="acute" output="x"/>`, 1),
		"text":       strings.Replace(sample, `<layouts>`, `<layouts>hello`, 1),
		"unclosed":   strings.Replace(sample, `</keyboard>`, ``, 1),
		"base cycle": strings.Replace(sample, `<keyMap index="0">`, `<keyMap index="0" baseMapSet="JIS" baseIndex="0">`, 1),
		"utf8":       "\xff\xfe\xfd",
		"bad maxout": strings.Replace(sample, `maxout="3"`, `maxout="x"`, 1),
		"empty state": strings.Replace(sample, `<when state="acute" output="´"/>
	</terminators>`, `<when state="" output="´"/>
	</terminators>`, 1),
	}
	for name, in := range cases {
		if k, r := Read([]byte(in), "e.keylayout"); k != nil || !r.HasErrors() {
			t.Errorf("%s: no error: %s", name, r.Summary())
		}
	}
}

func TestUTF16(t *testing.T) {
	var b []byte
	b = append(b, 0xFF, 0xFE)
	for _, r := range sample {
		if r > 0xFFFF {
			t.Fatal("sample must stay in the BMP for this test")
		}
		b = append(b, byte(r), byte(r>>8))
	}
	k := mustRead(t, b)
	if string(Write(k)) != string(Write(mustRead(t, []byte(sample)))) {
		t.Errorf("UTF-16 read differs from UTF-8")
	}
}
