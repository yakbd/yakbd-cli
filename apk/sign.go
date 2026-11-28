package apk

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sort"
	"unicode/utf8"
)

// Signing: JAR signing (v1, for Android 6 and older), then APK Signature Scheme v2 (Android 7 to 8.1) and v3
// (Android 9 and later) in an APK Signing Block before the central directory. Formats from AOSP's public pages
// "APK Signature Scheme v2/v3" and the JAR file specification; the v2 stripping-protection attribute id from AOSP's
// apksig (Apache-2.0), read for the fact only.

// Ids of the APK Signing Block pairs and attributes.
const (
	blockIDv2           = 0x7109871a
	blockIDv3           = 0xf05368c0
	attrStrippingProtID = 0xbeeff00d // v2 signed-data attribute: the APK is also signed with scheme 3
	v3MinSDK            = 28
	v3MaxSDK            = 0x7fffffff
	chunkSize           = 1 << 20
)

// v1 signature file names.
const (
	v1Manifest = "META-INF/MANIFEST.MF"
	v1SF       = "META-INF/CERT.SF"
)

// Sign returns the signed APK: the archive's entries (any earlier JAR signature dropped), a new JAR signature with
// SHA-256 digests (read by Android 4.3 and later), and v2 and v3 signatures. The archive is not changed.
func (a *Archive) Sign(s *Signer) ([]byte, error) {
	if s == nil {
		return nil, errors.New("apk: no signer")
	}
	b := &Archive{}
	for _, e := range a.Entries {
		if !IsSignatureFile(e.Name) {
			b.Entries = append(b.Entries, e)
		}
	}
	if len(b.Entries) == 0 {
		return nil, errors.New("apk: nothing to sign")
	}
	mf, sf, err := v1Files(b)
	if err != nil {
		return nil, err
	}
	sig, err := pkcs7(s, sf)
	if err != nil {
		return nil, err
	}
	blockName := "META-INF/CERT.RSA"
	if _, ok := s.key.(*ecdsa.PrivateKey); ok {
		blockName = "META-INF/CERT.EC"
	}
	b.Entries = append(b.Entries,
		&Entry{Name: v1Manifest, Data: mf}, &Entry{Name: v1SF, Data: sf}, &Entry{Name: blockName, Data: sig})
	z, cdStart, err := b.zipBytes()
	if err != nil {
		return nil, err
	}
	eocd := len(z) - 22
	digest := contentDigest(z[:cdStart], z[cdStart:eocd], z[eocd:])
	v2, err := signerBlock(s, digest, 2)
	if err != nil {
		return nil, err
	}
	v3, err := signerBlock(s, digest, 3)
	if err != nil {
		return nil, err
	}
	block := signingBlock([]idValue{{blockIDv2, v2}, {blockIDv3, v3}})
	out := make([]byte, 0, len(z)+len(block))
	out = append(out, z[:cdStart]...)
	out = append(out, block...)
	out = append(out, z[cdStart:]...)
	put32(out[len(out)-22+16:], uint32(cdStart+len(block)))
	return out, nil
}

// v1Files writes META-INF/MANIFEST.MF (a SHA-256 digest of each entry, sorted by name) and META-INF/CERT.SF (the
// digest of the manifest and of each of its sections; X-Android-APK-Signed names schemes 2 and 3, so a verifier
// that knows them refuses the APK with them stripped).
func v1Files(a *Archive) ([]byte, []byte, error) {
	var mf, sf bytes.Buffer
	mf.WriteString("Manifest-Version: 1.0\r\nCreated-By: yakbd\r\n\r\n")
	entries := append([]*Entry(nil), a.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	var sections [][2]string
	for _, e := range entries {
		if !utf8.ValidString(e.Name) {
			return nil, nil, errors.New("apk: entry name is not UTF-8")
		}
		var sec bytes.Buffer
		manifestLine(&sec, "Name: "+e.Name)
		d := sha256.Sum256(e.Data)
		manifestLine(&sec, "SHA-256-Digest: "+base64.StdEncoding.EncodeToString(d[:]))
		sec.WriteString("\r\n")
		mf.Write(sec.Bytes())
		sd := sha256.Sum256(sec.Bytes())
		sections = append(sections, [2]string{e.Name, base64.StdEncoding.EncodeToString(sd[:])})
	}
	md := sha256.Sum256(mf.Bytes())
	sf.WriteString("Signature-Version: 1.0\r\nCreated-By: yakbd\r\n")
	manifestLine(&sf, "SHA-256-Digest-Manifest: "+base64.StdEncoding.EncodeToString(md[:]))
	sf.WriteString("X-Android-APK-Signed: 2, 3\r\n\r\n")
	for _, s := range sections {
		manifestLine(&sf, "Name: "+s[0])
		manifestLine(&sf, "SHA-256-Digest: "+s[1])
		sf.WriteString("\r\n")
	}
	return mf.Bytes(), sf.Bytes(), nil
}

// manifestLine writes one manifest header: lines of at most 72 bytes, each continuation line starting with a
// space; never split inside a UTF-8 sequence.
func manifestLine(b *bytes.Buffer, s string) {
	limit := 72
	for len(s) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		b.WriteString(s[:cut])
		b.WriteString("\r\n ")
		s = s[cut:]
		limit = 71
	}
	b.WriteString(s)
	b.WriteString("\r\n")
}

// contentDigest is the v2/v3 digest of the three sections (entries, central directory, end record; the end
// record's central-directory offset is the offset of the signing block, which Sign inserts exactly there): the
// SHA-256 of 0x5a, the chunk count and the digests of the 1 MiB chunks, each the SHA-256 of 0xa5, its length and
// its bytes.
func contentDigest(sections ...[]byte) []byte {
	var digests []byte
	n := 0
	for _, sec := range sections {
		for off := 0; off < len(sec); off += chunkSize {
			end := min(off+chunkSize, len(sec))
			h := sha256.New()
			var pre [5]byte
			pre[0] = 0xa5
			put32(pre[1:], uint32(end-off))
			h.Write(pre[:])
			h.Write(sec[off:end])
			digests = h.Sum(digests)
			n++
		}
	}
	h := sha256.New()
	var pre [5]byte
	pre[0] = 0x5a
	put32(pre[1:], uint32(n))
	h.Write(pre[:])
	h.Write(digests)
	return h.Sum(nil)
}

// lp prefixes b with its length (uint32).
func lp(parts ...[]byte) []byte {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := appendU32(make([]byte, 0, 4+n), uint32(n))
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func u32(v uint32) []byte { return appendU32(nil, v) }

// signerBlock is the value of a v2 or v3 block: a sequence of one signer.
func signerBlock(s *Signer, digest []byte, scheme int) ([]byte, error) {
	alg := s.alg()
	digests := lp(lp(u32(alg), lp(digest)))
	certs := lp(lp(s.certDER))
	var signed []byte
	if scheme == 2 {
		attrs := lp(lp(u32(attrStrippingProtID), u32(3)))
		signed = append(append(digests, certs...), attrs...)
	} else {
		signed = append(append(digests, certs...), u32(v3MinSDK)...)
		signed = append(append(signed, u32(v3MaxSDK)...), lp()...)
	}
	sig, err := s.sign(signed)
	if err != nil {
		return nil, err
	}
	sigs := lp(lp(u32(alg), lp(sig)))
	var signer []byte
	if scheme == 2 {
		signer = lp(lp(signed), sigs, lp(s.publicKey()))
	} else {
		signer = lp(lp(signed), u32(v3MinSDK), u32(v3MaxSDK), sigs, lp(s.publicKey()))
	}
	return lp(signer), nil
}

type idValue struct {
	id    uint32
	value []byte
}

// signingBlock is the APK Signing Block: its size (uint64, without this field), the pairs (each a uint64 length,
// a uint32 id and the value), the size again and the magic "APK Sig Block 42".
func signingBlock(pairs []idValue) []byte {
	var body []byte
	for _, p := range pairs {
		var h [12]byte
		put64(h[:], uint64(4+len(p.value)))
		put32(h[8:], p.id)
		body = append(append(body, h[:]...), p.value...)
	}
	size := uint64(len(body) + 8 + 16)
	out := make([]byte, 8, 8+len(body)+24)
	put64(out, size)
	out = append(out, body...)
	var tail [8]byte
	put64(tail[:], size)
	out = append(out, tail[:]...)
	return append(out, "APK Sig Block 42"...)
}
