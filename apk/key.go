package apk

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"
)

// Signer is a signing identity: a private key and its X.509 certificate. Android compares the certificate when
// an APK is updated, and a builder registers the certificate's SHA-256 fingerprint for developer verification, so
// the same key must always come with the same certificate.
type Signer struct {
	key     crypto.Signer
	certDER []byte
	cert    *x509.Certificate
	// TestKey is set for a key Generate made: nobody can update an APK signed with it from a later build.
	TestKey bool
}

// Signature algorithm ids of APK Signature Scheme v2/v3.
const (
	algRSAPKCS1SHA256 = 0x0103
	algECDSASHA256    = 0x0201
)

// LoadSigner reads a signing key from a PEM file: a private key ("PRIVATE KEY", PKCS #8; also "RSA PRIVATE KEY"
// and "EC PRIVATE KEY") and optionally its certificate ("CERTIFICATE", for example exported from the builder's
// keystore). An RSA key without a certificate gets one derived from the key alone (DerivedCertificate), the same
// on every build. An EC key needs its certificate in the file: an ECDSA signature is never the same twice, so a
// derived certificate would change with each build.
func LoadSigner(path string) (*Signer, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s, err := ParseSigner(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	return s, nil
}

// ParseSigner is LoadSigner on the file's content.
func ParseSigner(pemData []byte) (*Signer, error) {
	var key crypto.Signer
	var certDER []byte
	for rest := pemData; ; {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		switch blk.Type {
		case "PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY":
			if key != nil {
				return nil, errors.New("more than one private key")
			}
			k, err := parseKey(blk)
			if err != nil {
				return nil, err
			}
			key = k
		case "CERTIFICATE":
			if certDER == nil {
				certDER = blk.Bytes // the first certificate is the signer's; others are its chain
			}
		case "ENCRYPTED PRIVATE KEY":
			return nil, errors.New("the private key is encrypted: export it without a password (openssl pkcs8 -nocrypt)")
		}
	}
	if key == nil {
		return nil, errors.New("no private key (PEM block PRIVATE KEY)")
	}
	if certDER == nil {
		if _, ok := key.(*rsa.PrivateKey); !ok {
			return nil, errors.New("an EC key needs its certificate in the same file (PEM block CERTIFICATE)")
		}
		der, err := DerivedCertificate(key)
		if err != nil {
			return nil, err
		}
		certDER = der
	}
	return newSigner(key, certDER)
}

func parseKey(blk *pem.Block) (crypto.Signer, error) {
	var k any
	var err error
	switch blk.Type {
	case "RSA PRIVATE KEY":
		k, err = x509.ParsePKCS1PrivateKey(blk.Bytes)
	case "EC PRIVATE KEY":
		k, err = x509.ParseECPrivateKey(blk.Bytes)
	default:
		k, err = x509.ParsePKCS8PrivateKey(blk.Bytes)
	}
	if err != nil {
		return nil, err
	}
	switch k := k.(type) {
	case *rsa.PrivateKey:
		if k.N.BitLen() < 2048 {
			return nil, errors.New("an RSA key needs at least 2048 bits")
		}
		return k, nil
	case *ecdsa.PrivateKey:
		if k.Curve != elliptic.P256() {
			return nil, errors.New("an EC key must be on the curve P-256")
		}
		return k, nil
	}
	return nil, fmt.Errorf("unsupported key type %T (RSA or EC P-256)", k)
}

func newSigner(key crypto.Signer, certDER []byte) (*Signer, error) {
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, fmt.Errorf("certificate: %v", err)
	}
	pub, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		return nil, err
	}
	cpub, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil || !bytes.Equal(pub, cpub) {
		return nil, errors.New("the certificate is not the private key's")
	}
	return &Signer{key: key, certDER: certDER, cert: cert}, nil
}

// derivedValidity is the validity of a derived certificate. Google Play wants signing certificates valid past
// 2033-10-22; a hundred years covers every use.
var derivedValidity = [2]time.Time{
	time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	time.Date(2126, 1, 1, 0, 0, 0, 0, time.UTC),
}

// DerivedCertificate makes the self-signed certificate of an RSA key from the key alone: subject "CN=Keyboard
// layout", serial number from the SHA-256 of the public key, fixed validity 2026-2126. RSA PKCS #1 v1.5 signatures
// are deterministic, so the same key always gives the same certificate and fingerprint.
func DerivedCertificate(key crypto.Signer) ([]byte, error) {
	if _, ok := key.(*rsa.PrivateKey); !ok {
		return nil, errors.New("a derived certificate needs an RSA key")
	}
	pub, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(pub)
	serial := new(big.Int).SetBytes(h[:8])
	serial.SetBit(serial, 63, 0)
	serial.SetBit(serial, 62, 1) // positive, always eight bytes
	name := pkix.Name{CommonName: "Keyboard layout"}
	tmpl := &x509.Certificate{
		SerialNumber:       serial,
		Subject:            name,
		Issuer:             name,
		NotBefore:          derivedValidity[0],
		NotAfter:           derivedValidity[1],
		SignatureAlgorithm: x509.SHA256WithRSA,
		SubjectKeyId:       h[:20],
		AuthorityKeyId:     h[:20],
	}
	return x509.CreateCertificate(noRand{}, tmpl, tmpl, key.Public(), key)
}

// noRand is the randomness of a derived certificate: none is needed (the serial number is given, and RSA PKCS #1
// v1.5 does not use any), and an attempt to read it is an error rather than a silent difference.
type noRand struct{}

func (noRand) Read([]byte) (int, error) {
	return 0, errors.New("apk: derived certificate needs no randomness")
}

// GenerateSigner makes a fresh RSA 2048 key with a derived certificate: a test key. Nothing keeps it: an APK
// signed with it can never be updated by a later build, so a build with a test key says so (ReportSigned).
func GenerateSigner() (*Signer, error) {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	der, err := DerivedCertificate(k)
	if err != nil {
		return nil, err
	}
	s, err := newSigner(k, der)
	if err != nil {
		return nil, err
	}
	s.TestKey = true
	return s, nil
}

// Fingerprint is the SHA-256 of the signing certificate as upper-case hex pairs joined by ':', the form Android's
// tools and the developer verification console show.
func (s *Signer) Fingerprint() string {
	h := sha256.Sum256(s.certDER)
	var b strings.Builder
	for i, c := range h {
		if i > 0 {
			b.WriteByte(':')
		}
		fmt.Fprintf(&b, "%02X", c)
	}
	return b.String()
}

// Certificate returns the signing certificate (DER).
func (s *Signer) Certificate() []byte { return append([]byte(nil), s.certDER...) }

// PEM returns the key (PKCS #8) and certificate as a PEM file that LoadSigner reads.
func (s *Signer) PEM() ([]byte, error) {
	k, err := x509.MarshalPKCS8PrivateKey(s.key)
	if err != nil {
		return nil, err
	}
	out := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: k})
	return append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.certDER})...), nil
}

// alg is the v2/v3 signature algorithm id of the key.
func (s *Signer) alg() uint32 {
	if _, ok := s.key.(*ecdsa.PrivateKey); ok {
		return algECDSASHA256
	}
	return algRSAPKCS1SHA256
}

// sign signs the SHA-256 of data.
func (s *Signer) sign(data []byte) ([]byte, error) {
	h := sha256.Sum256(data)
	return s.key.Sign(rand.Reader, h[:], crypto.SHA256)
}

// publicKey is the SubjectPublicKeyInfo (DER) of the key.
func (s *Signer) publicKey() []byte {
	b, _ := x509.MarshalPKIXPublicKey(s.key.Public())
	return b
}
