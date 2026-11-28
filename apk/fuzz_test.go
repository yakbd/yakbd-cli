package apk

import (
	"bytes"
	"errors"
	"testing"

	"github.com/yakbd/yakbd-cli/internal/fuzzcheck"
)

// FuzzParseXML: Android binary XML (a runtime APK's manifest is outside input). A document the reader accepts
// writes, and write → read → write is stable.
func FuzzParseXML(f *testing.F) {
	b, err := EncodeXML(testManifest())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(b)
	small, _ := EncodeXML(E("a", []*Attr{{Name: "x", Value: String("y")}}, &Node{IsText: true, Text: "t", TextValue: String("t")}))
	f.Add(small)
	fuzzcheck.Seeds(f, "testdata/*.axml")
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzcheck.Check(t, fuzzcheck.Default, len(data), func() error {
			n, err := ParseXML(data)
			if err != nil {
				return nil
			}
			out, err := EncodeXML(n)
			if err != nil {
				return nil // a parsed document the writer refuses (an element without a name)
			}
			n2, err := ParseXML(out)
			if err != nil {
				return errors.New("the written form does not read: " + err.Error())
			}
			out2, err := EncodeXML(n2)
			if err != nil || !bytes.Equal(out, out2) {
				return errors.New("write → read → write is not stable")
			}
			return nil
		})
	})
}
