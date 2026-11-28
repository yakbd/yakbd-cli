// Command apkcheck writes the APKs of the apk package's CI control (docs/apk.md §4): a code-free test APK with a
// keyboard-layout receiver, built from scratch, and the repackaging of an APK made by aapt2. The CI job checks them
// with the Android SDK's apksigner and aapt2; nothing here needs the SDK.
//
//	go run ./tools/apkcheck -o DIR                                 # DIR/kcm-test.apk (test key), DIR/kcm-test-ec.apk (EC key)
//	go run ./tools/apkcheck -rewrite IN.apk -package P -label L -o OUT.apk
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/yakbd/yakbd-cli/apk"
)

func main() {
	out := flag.String("o", "", "output folder (or file with -rewrite)")
	rewrite := flag.String("rewrite", "", "an APK to repackage")
	pkg := flag.String("package", "", "-rewrite: the new package name")
	label := flag.String("label", "", "-rewrite: the new application label")
	asset := flag.String("asset", "", "-rewrite: an asset to add, name=text (assets/<name>)")
	flag.Parse()
	if *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*out, *rewrite, *pkg, *label, *asset); err != nil {
		fmt.Fprintln(os.Stderr, "apkcheck:", err)
		os.Exit(1)
	}
}

func run(out, rewrite, pkg, label, asset string) error {
	if rewrite != "" {
		in, err := os.ReadFile(rewrite)
		if err != nil {
			return err
		}
		s, err := apk.GenerateSigner()
		if err != nil {
			return err
		}
		c := apk.Changes{Package: pkg, Label: label}
		if asset != "" {
			for i := 0; i < len(asset); i++ {
				if asset[i] == '=' {
					c.Files = map[string][]byte{"assets/" + asset[:i]: []byte(asset[i+1:])}
					break
				}
			}
		}
		b, old, err := apk.Repackage(in, c, s)
		if err != nil {
			return err
		}
		fmt.Printf("%s: %s -> %s, certificate SHA-256 %s\n", out, old, pkg, s.Fingerprint())
		return os.WriteFile(out, b, 0o644)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	a, err := KCMTest("org.yakbd.test.kcm")
	if err != nil {
		return err
	}
	s, err := apk.GenerateSigner()
	if err != nil {
		return err
	}
	b, err := a.Sign(s)
	if err != nil {
		return err
	}
	p := filepath.Join(out, "kcm-test.apk")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		return err
	}
	fmt.Printf("%s: certificate SHA-256 %s\n", p, s.Fingerprint())
	ec, err := ecSigner()
	if err != nil {
		return err
	}
	a, err = KCMTest("org.yakbd.test.kcm.ec")
	if err != nil {
		return err
	}
	if b, err = a.Sign(ec); err != nil {
		return err
	}
	p = filepath.Join(out, "kcm-test-ec.apk")
	fmt.Printf("%s: certificate SHA-256 %s\n", p, ec.Fingerprint())
	return os.WriteFile(p, b, 0o644)
}

// KCMTest is the code-free test APK: one receiver of QUERY_KEYBOARD_LAYOUTS whose meta-data names
// res/xml/keyboard_layouts.xml, which names one key character map res/raw/test.kcm.
func KCMTest(pkg string) (*apk.Archive, error) {
	table, ids, err := apk.ResourceTable(pkg, []apk.Resource{
		{Type: "xml", Name: "keyboard_layouts", File: "res/xml/keyboard_layouts.xml"},
		{Type: "raw", Name: "test", File: "res/raw/test.kcm"},
	})
	if err != nil {
		return nil, err
	}
	ns := []apk.Namespace{{Prefix: "android", URI: apk.AndroidNS}}
	man := apk.E("manifest", []*apk.Attr{
		{Name: "package", Value: apk.String(pkg)},
		apk.A("versionCode", apk.Int(1)),
		apk.A("versionName", apk.String("1.0")),
	},
		apk.E("uses-sdk", []*apk.Attr{apk.A("minSdkVersion", apk.Int(21)), apk.A("targetSdkVersion", apk.Int(34))}),
		apk.E("application", []*apk.Attr{apk.A("label", apk.String("YAKBD test layouts")), apk.A("hasCode", apk.Bool(false))},
			apk.E("receiver", []*apk.Attr{apk.A("name", apk.String(pkg+".Layouts")), apk.A("label", apk.String("YAKBD test layouts")), apk.A("exported", apk.Bool(true))},
				apk.E("intent-filter", nil,
					apk.E("action", []*apk.Attr{apk.A("name", apk.String("android.hardware.input.action.QUERY_KEYBOARD_LAYOUTS"))})),
				apk.E("meta-data", []*apk.Attr{
					apk.A("name", apk.String("android.hardware.input.metadata.KEYBOARD_LAYOUTS")),
					apk.A("resource", apk.Ref(ids["xml/keyboard_layouts"])),
				}))))
	man.Namespaces = ns
	mb, err := apk.EncodeXML(man)
	if err != nil {
		return nil, err
	}
	kl := apk.E("keyboard-layouts", nil,
		apk.E("keyboard-layout", []*apk.Attr{
			apk.A("name", apk.String("test")),
			apk.A("label", apk.String("YAKBD test")),
			apk.A("keyboardLayout", apk.Ref(ids["raw/test"])),
		}))
	kl.Namespaces = ns
	kb, err := apk.EncodeXML(kl)
	if err != nil {
		return nil, err
	}
	kcm := "# A test key character map: A types b, Shift+A types B.\ntype OVERLAY\n\nkey A {\n    label: 'B'\n    base: 'b'\n    shift, capslock: 'B'\n}\n"
	return &apk.Archive{Entries: []*apk.Entry{
		{Name: "AndroidManifest.xml", Data: mb},
		{Name: "resources.arsc", Data: table, Store: true},
		{Name: "res/xml/keyboard_layouts.xml", Data: kb},
		{Name: "res/raw/test.kcm", Data: []byte(kcm)},
	}}, nil
}

// ecSigner makes an EC P-256 key with a self-signed certificate.
func ecSigner() (*apk.Signer, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "EC test"},
		NotBefore: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2126, 1, 1, 0, 0, 0, 0, time.UTC)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		return nil, err
	}
	kd, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, err
	}
	p := append(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	return apk.ParseSigner(p)
}
