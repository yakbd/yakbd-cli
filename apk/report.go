package apk

import (
	"flag"

	"github.com/yakbd/yakbd-cli/loss"
)

// Report kinds of a signed build (docs/report-file.md §7).
const (
	// KindSigned is an info entry for each APK written: detail.file, detail.package, detail.certificateSha256
	// (Signer.Fingerprint: what a builder registers for Android developer verification) and detail.testKey.
	KindSigned = "apk.signed"
	// KindTestKey is a warning: the APK is signed with a generated test key, which no later build has.
	KindTestKey = "apk.testKey"
)

// ReportSigned adds the signing facts of the APK file of package pkg to r.
func ReportSigned(r *loss.Report, file, pkg string, s *Signer) {
	r.Entries = append(r.Entries, loss.Entry{Class: loss.Diagnostic, Severity: loss.Info, Code: KindSigned,
		Where: loss.Where{Path: file}, Message: "package " + pkg + " signed by the certificate with SHA-256 " + s.Fingerprint(),
		Detail: map[string]any{"file": file, "package": pkg, "certificateSha256": s.Fingerprint(), "testKey": s.TestKey}})
	if s.TestKey {
		r.Warnf(KindTestKey, loss.Where{Path: file}, "signed with a generated test key: a later build cannot update this APK (its key is gone); sign with your own key: -key <file.pem>")
	}
}

// KeyFlag registers the shared -key flag of the Android targets on fs (docs/targets.md §5).
func KeyFlag(fs *flag.FlagSet) *string {
	return fs.String("key", "", "the signing key: a PEM file with a PKCS #8 private key (RSA 2048+ or EC P-256) and, optionally, its certificate (required for EC); without it, a generated test key")
}

// SignerFor returns the signer of a -key value: LoadSigner(path), or GenerateSigner() for "".
func SignerFor(path string) (*Signer, error) {
	if path == "" {
		return GenerateSigner()
	}
	return LoadSigner(path)
}
