package apk

import (
	"archive/zip"
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"flag"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yakbd/yakbd-cli/loss"
)

var update = flag.Bool("update", false, "rewrite the golden files of testdata/")

var ns = []Namespace{{Prefix: "android", URI: AndroidNS}}

// testManifest is a manifest with the names RenamePackage handles.
func testManifest() *Node {
	m := E("manifest", []*Attr{
		{Name: "package", Value: String("org.example.rt")},
		A("versionCode", Int(3)),
		A("versionName", String("1.2")),
	},
		E("uses-sdk", []*Attr{A("minSdkVersion", Int(21)), A("targetSdkVersion", Int(34))}),
		E("permission", []*Attr{A("name", String("org.example.rt.DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION")), A("protectionLevel", Value{Type: TypeIntHex, Data: 2})}),
		E("uses-permission", []*Attr{A("name", String("org.example.rt.DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION"))}),
		E("uses-permission", []*Attr{A("name", String("android.permission.VIBRATE"))}),
		E("application", []*Attr{A("label", Ref(0x7f010000)), A("hasCode", Bool(true)), {Name: "name", Value: String("not an android attribute")}},
			E("service", []*Attr{A("name", String(".Ime")), A("permission", String("android.permission.BIND_INPUT_METHOD")), A("exported", Bool(true))},
				E("intent-filter", nil, E("action", []*Attr{A("name", String("android.view.InputMethod"))})),
				E("meta-data", []*Attr{A("name", String("android.view.im")), A("resource", Ref(0x7f020000))})),
			E("activity", []*Attr{A("name", String("Settings")), A("process", String("org.example.rt.settings"))}),
			E("provider", []*Attr{A("name", String("androidx.startup.InitializationProvider")),
				A("authorities", String("org.example.rt.androidx-startup;org.example.rtx.other"))}),
		))
	m.Namespaces = ns
	return m
}

func TestXMLRoundTrip(t *testing.T) {
	b, err := EncodeXML(testManifest())
	if err != nil {
		t.Fatal(err)
	}
	n, err := ParseXML(b)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := EncodeXML(n)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, b2) {
		t.Fatal("encode(parse(x)) != x")
	}
	// The attribute "name" without an id (on <application>) must not share the slot of android:name.
	app := n.Elements("application")[0]
	if a := app.Attr("", "name"); a == nil || a.ResID != 0 || a.Value.Str != "not an android attribute" {
		t.Errorf("plain name attribute: %+v", a)
	}
	if a := app.Attr(AndroidNS, "label"); a == nil || a.ResID != 0x01010001 || a.Value != Ref(0x7f010000) {
		t.Errorf("label: %+v", a)
	}
	// Sorted by resource id, the one without an id last.
	if got := app.Attrs; got[0].Name != "label" || got[1].Name != "hasCode" || got[2].ResID != 0 {
		t.Errorf("attribute order %s %s %s", got[0].Name, got[1].Name, got[2].Name)
	}
	golden(t, "manifest.axml", b)
}

// golden compares b with testdata/<name> (-update rewrites it).
func golden(t *testing.T, name string, b []byte) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, b) {
		t.Errorf("%s differs from the golden file (go test ./apk -update after a deliberate change)", name)
	}
}

func TestXMLStructure(t *testing.T) {
	b, err := EncodeXML(testManifest())
	if err != nil {
		t.Fatal(err)
	}
	// Chunks: XML header, string pool, resource map, then nodes; the resource map lists ascending ids, and the
	// pool's first strings are their names.
	if le16(b) != chunkXML || int(le32(b[4:])) != len(b) {
		t.Fatal("header")
	}
	off := 8
	if le16(b[off:]) != chunkStringPool {
		t.Fatal("no pool first")
	}
	pool, err := parsePool(b[off : off+int(le32(b[off+4:]))])
	if err != nil {
		t.Fatal(err)
	}
	off += int(le32(b[off+4:]))
	if le16(b[off:]) != chunkXMLResMap {
		t.Fatal("no resource map second")
	}
	n := (int(le32(b[off+4:])) - 8) / 4
	prev := uint32(0)
	for i := 0; i < n; i++ {
		id := le32(b[off+8+4*i:])
		if id <= prev {
			t.Errorf("resource map not ascending at %d", i)
		}
		prev = id
		if AndroidAttr(pool[i]) != id {
			t.Errorf("slot %d: %s has id %08x", i, pool[i], id)
		}
	}
	off += int(le32(b[off+4:]))
	var kinds []uint16
	for off < len(b) {
		kinds = append(kinds, le16(b[off:]))
		off += int(le32(b[off+4:]))
	}
	if kinds[0] != chunkXMLStartNS || kinds[len(kinds)-1] != chunkXMLEndNS || kinds[1] != chunkXMLStart || kinds[len(kinds)-2] != chunkXMLEnd {
		t.Errorf("node chunks %x", kinds)
	}
}

func TestParseXMLRefuses(t *testing.T) {
	good, _ := EncodeXML(testManifest())
	for name, b := range map[string][]byte{
		"empty":     nil,
		"truncated": good[:len(good)-4],
		"not xml":   append([]byte{2, 0}, good[2:]...),
	} {
		if _, err := ParseXML(b); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestUTF8Pool(t *testing.T) {
	in := []string{"a", "", "é ü", strings.Repeat("x", 200), "\U0001F600"}
	for _, u8 := range []bool{false, true} {
		out, err := parsePool(encodePool(in, u8))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(out, "|") != strings.Join(in, "|") {
			t.Errorf("utf8=%v: %q", u8, out)
		}
	}
}

func TestRenamePackage(t *testing.T) {
	m := testManifest()
	old, err := RenamePackage(m, "org.yakbd.layout.fo")
	if err != nil || old != "org.example.rt" {
		t.Fatal(old, err)
	}
	if err := SetLabel(m, "Føroyskt"); err != nil {
		t.Fatal(err)
	}
	b, _ := EncodeXML(m)
	m, _ = ParseXML(b)
	want := map[string]string{
		"manifest@package":                  "org.yakbd.layout.fo",
		"permission@name":                   "org.yakbd.layout.fo.DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION",
		"uses-permission@name":              "org.yakbd.layout.fo.DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION|android.permission.VIBRATE",
		"application@label":                 "Føroyskt",
		"service@name":                      "org.example.rt.Ime",
		"service@permission":                "android.permission.BIND_INPUT_METHOD",
		"activity@name":                     "org.example.rt.Settings",
		"activity@process":                  "org.yakbd.layout.fo.settings",
		"provider@name":                     "androidx.startup.InitializationProvider",
		"provider@authorities":              "org.yakbd.layout.fo.androidx-startup;org.example.rtx.other",
		"action@name":                       "android.view.InputMethod",
		"meta-data@name":                    "android.view.im",
		"application@name (no namespace)":   "not an android attribute",
		"manifest@versionName (unaffected)": "1.2",
	}
	got := map[string][]string{}
	m.Walk(func(n *Node) {
		for _, a := range n.Attrs {
			if a.Value.Type != TypeString {
				continue
			}
			k := n.Name + "@" + a.Name
			if a.NS == "" && n.Name != "manifest" {
				k += " (no namespace)"
			}
			if a.Name == "versionName" {
				k += " (unaffected)"
			}
			got[k] = append(got[k], a.Value.Str)
		}
	})
	for k, v := range want {
		if g := strings.Join(got[k], "|"); g != v {
			t.Errorf("%s = %q, want %q", k, g, v)
		}
	}
	if _, err := RenamePackage(m, "nodots"); err == nil {
		t.Error("a package name without a dot accepted")
	}
}

func TestZipAlignment(t *testing.T) {
	a := &Archive{}
	for i, name := range []string{"a", "bb/c.bin", "lib/x86_64/libx.so", "resources.arsc", "d.txt", "eeeee"} {
		a.Entries = append(a.Entries, &Entry{Name: name, Data: bytes.Repeat([]byte{byte(i)}, 37*i+1), Store: i%2 == 0 || name == "resources.arsc"})
	}
	b, err := a.Unsigned()
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Method != zip.Store {
			continue
		}
		off, err := f.DataOffset()
		if err != nil {
			t.Fatal(err)
		}
		if off%int64(Align(f.Name)) != 0 {
			t.Errorf("%s at %d, not aligned to %d", f.Name, off, Align(f.Name))
		}
	}
	back, err := Read(b)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range back.Entries {
		if e.Name != a.Entries[i].Name || !bytes.Equal(e.Data, a.Entries[i].Data) || e.Store != a.Entries[i].Store {
			t.Errorf("entry %d differs", i)
		}
	}
	b2, _ := back.Unsigned()
	if !bytes.Equal(b, b2) {
		t.Error("read and written again: other bytes")
	}
}

func TestResourceTable(t *testing.T) {
	b, ids, err := ResourceTable("org.yakbd.t", []Resource{
		{Type: "xml", Name: "layouts", File: "res/xml/layouts.xml"},
		{Type: "raw", Name: "fo", File: "res/raw/fo.kcm"},
		{Type: "raw", Name: "de", File: "res/raw/de.kcm"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ids["xml/layouts"] != 0x7f010000 || ids["raw/de"] != 0x7f020000 || ids["raw/fo"] != 0x7f020001 {
		t.Errorf("ids %x", ids)
	}
	if p, id, err := TablePackage(b); err != nil || p != "org.yakbd.t" || id != 0x7f {
		t.Errorf("package %q %x %v", p, id, err)
	}
	golden(t, "resources.arsc", b)
	if _, _, err := ResourceTable("p.q", []Resource{{Type: "raw", Name: "Bad-Name", File: "x"}}); err == nil {
		t.Error("bad name accepted")
	}
}

// rsaPEM is an RSA 2048 key made once per test run, as a PEM file with the key only (no private key is ever
// committed, not even a test key).
func rsaPEM(t *testing.T) []byte {
	t.Helper()
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		d, _ := x509.MarshalPKCS8PrivateKey(k)
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: d})
	})
	return keyPEM
}

var (
	keyOnce sync.Once
	keyPEM  []byte
)

func TestDerivedCertificateStable(t *testing.T) {
	p := rsaPEM(t)
	s1, err := ParseSigner(p)
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := ParseSigner(p)
	if s1.Fingerprint() != s2.Fingerprint() || s1.TestKey {
		t.Fatalf("two loads, two fingerprints: %s %s", s1.Fingerprint(), s2.Fingerprint())
	}
	// PEM() writes the certificate along: the same identity again.
	out, _ := s1.PEM()
	s3, err := ParseSigner(out)
	if err != nil || s3.Fingerprint() != s1.Fingerprint() {
		t.Errorf("PEM round trip: %v", err)
	}
	// An EC key needs its certificate.
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	kd, _ := x509.MarshalPKCS8PrivateKey(k)
	if _, err := ParseSigner(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd})); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Errorf("EC without certificate: %v", err)
	}
	// A certificate of another key.
	g, _ := GenerateSigner()
	other := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: g.Certificate()}), p...)
	if _, err := ParseSigner(other); err == nil {
		t.Error("foreign certificate accepted")
	}
	if !g.TestKey {
		t.Error("generated key not marked")
	}
}

func testAPK(t *testing.T) *Archive {
	m, err := EncodeXML(testManifest())
	if err != nil {
		t.Fatal(err)
	}
	arsc, _, _ := ResourceTable("org.example.rt", []Resource{{Type: "raw", Name: "x", File: "res/raw/x"}})
	return &Archive{Entries: []*Entry{
		{Name: "AndroidManifest.xml", Data: m},
		{Name: "resources.arsc", Data: arsc, Store: true},
		{Name: "res/raw/x", Data: []byte("x")},
		{Name: "assets/" + strings.Repeat("long-name-", 10) + "ä.json", Data: []byte("{}")},
		{Name: "META-INF/OLD.RSA", Data: []byte("an old signature")},
	}}
}

func TestSign(t *testing.T) {
	g, err := GenerateSigner()
	if err != nil {
		t.Fatal(err)
	}
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "t"}, NotBefore: time.Unix(0, 0), NotAfter: time.Unix(1<<33, 0)}
	cd, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	kd, _ := x509.MarshalPKCS8PrivateKey(k)
	ec, err := ParseSigner(append(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cd})...))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Signer{g, ec} {
		b, err := testAPK(t).Sign(s)
		if err != nil {
			t.Fatal(err)
		}
		verify(t, b, s)
	}
	// RSA signing is deterministic: the same key gives the same bytes.
	s, _ := ParseSigner(rsaPEM(t))
	b1, _ := testAPK(t).Sign(s)
	b2, _ := testAPK(t).Sign(s)
	if !bytes.Equal(b1, b2) {
		t.Error("two signatures with one RSA key differ")
	}
}

// verify checks a signed APK as a verifier would: v1 digests, the APK Signing Block, the v2 and v3 digests and
// signatures with the certificate's key.
func verify(t *testing.T, b []byte, s *Signer) {
	t.Helper()
	a, err := Read(b)
	if err != nil {
		t.Fatal(err)
	}
	if a.Get("META-INF/OLD.RSA") != nil {
		t.Error("old signature kept")
	}
	mf, sf := a.Get(v1Manifest), a.Get(v1SF)
	if mf == nil || sf == nil {
		t.Fatal("no v1 files")
	}
	md := sha256.Sum256(mf.Data)
	if !strings.Contains(unfold(sf.Data), "SHA-256-Digest-Manifest: "+base64.StdEncoding.EncodeToString(md[:])) {
		t.Error("SF: manifest digest")
	}
	for _, e := range a.Entries {
		if IsSignatureFile(e.Name) {
			continue
		}
		d := sha256.Sum256(e.Data)
		if !strings.Contains(unfold(mf.Data), "Name: "+e.Name+"\r\nSHA-256-Digest: "+base64.StdEncoding.EncodeToString(d[:])) {
			t.Errorf("MANIFEST.MF: %s", e.Name)
		}
	}
	for _, line := range strings.Split(string(mf.Data), "\r\n") {
		if len(line) > 72 {
			t.Errorf("manifest line of %d bytes", len(line))
		}
	}
	// The signing block sits right before the central directory.
	eocd := len(b) - 22
	cd := int(le32(b[eocd+16:]))
	if string(b[cd-16:cd]) != "APK Sig Block 42" {
		t.Fatal("no signing block")
	}
	size := int(le64(b[cd-24:]))
	start := cd - size - 8
	if int(le64(b[start:])) != size {
		t.Fatal("block sizes differ")
	}
	// The digest: the entries up to the block, the central directory, the end record with the block's offset.
	end := append([]byte(nil), b[eocd:]...)
	put32(end[16:], uint32(start))
	want := contentDigest(b[:start], b[cd:eocd], end)
	pairs := map[uint32][]byte{}
	for p := start + 8; p < cd-24; {
		n := int(le64(b[p:]))
		pairs[le32(b[p+8:])] = b[p+12 : p+8+n]
		p += 8 + n
	}
	for _, scheme := range []uint32{blockIDv2, blockIDv3} {
		v := pairs[scheme]
		if v == nil {
			t.Fatalf("no block %x", scheme)
		}
		signers := field(t, v)
		signer := field(t, signers)
		signed := field(t, signer)
		rest := signer[4+len(signed):]
		if scheme == blockIDv3 {
			if le32(rest) != v3MinSDK || le32(rest[4:]) != v3MaxSDK {
				t.Error("v3 sdk range")
			}
			rest = rest[8:]
		}
		sigs := field(t, rest)
		pub := field(t, rest[4+len(sigs):])
		if !bytes.Equal(pub, s.publicKey()) {
			t.Error("public key")
		}
		digests := field(t, signed)
		dg := field(t, digests)
		if le32(dg) != s.alg() || !bytes.Equal(field(t, dg[4:]), want) {
			t.Errorf("block %x: digest", scheme)
		}
		certs := field(t, signed[4+len(digests):])
		if !bytes.Equal(field(t, certs), s.certDER) {
			t.Error("certificate")
		}
		sg := field(t, sigs)
		sig := field(t, sg[4:])
		h := sha256.Sum256(signed)
		switch k := s.cert.PublicKey.(type) {
		case *rsa.PublicKey:
			if err := rsa.VerifyPKCS1v15(k, crypto.SHA256, h[:], sig); err != nil {
				t.Errorf("block %x: %v", scheme, err)
			}
		case *ecdsa.PublicKey:
			if !ecdsa.VerifyASN1(k, h[:], sig) {
				t.Errorf("block %x: EC signature", scheme)
			}
		}
	}
}

func le64(b []byte) uint64 { return uint64(le32(b)) | uint64(le32(b[4:]))<<32 }

// field returns the length-prefixed field at the start of b.
func field(t *testing.T, b []byte) []byte {
	t.Helper()
	if len(b) < 4 || int(le32(b)) > len(b)-4 {
		t.Fatal("field out of range")
	}
	return b[4 : 4+le32(b)]
}

// unfold joins manifest continuation lines.
func unfold(b []byte) string { return strings.ReplaceAll(string(b), "\r\n ", "") }

func TestRepackage(t *testing.T) {
	s, _ := ParseSigner(rsaPEM(t))
	first, err := testAPK(t).Sign(s)
	if err != nil {
		t.Fatal(err)
	}
	g, _ := GenerateSigner()
	out, old, err := Repackage(first, Changes{Package: "org.yakbd.fo", Label: "Føroyskt", Files: map[string][]byte{"assets/layout.json": []byte(`{"a":1}`)}}, g)
	if err != nil || old != "org.example.rt" {
		t.Fatal(old, err)
	}
	verify(t, out, g)
	a, _ := Read(out)
	m, err := ParseXML(a.Get("AndroidManifest.xml").Data)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := ManifestPackage(m); p != "org.yakbd.fo" {
		t.Error(p)
	}
	if string(a.Get("assets/layout.json").Data) != `{"a":1}` || !a.Get("resources.arsc").Store {
		t.Error("files")
	}
	if a.Get("META-INF/CERT.RSA") == nil || len(a.Entries) != 8 {
		t.Errorf("entries %v", a.Names())
	}
	var r loss.Report
	ReportSigned(&r, "x.apk", "org.yakbd.fo", g)
	if len(r.Entries) != 2 || r.Entries[0].Code != KindSigned || r.Entries[0].Detail["certificateSha256"] != g.Fingerprint() || r.Entries[1].Code != KindTestKey || r.Entries[1].Severity != loss.Warning {
		t.Errorf("report %+v", r.Entries)
	}
}
