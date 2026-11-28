package apk

import (
	"crypto/ecdsa"
	"math/big"
)

// pkcs7 is the signature block file of a JAR signature (META-INF/CERT.RSA or .EC): a PKCS #7 / CMS SignedData
// (RFC 2315) with the signer's certificate and one SignerInfo over the .SF file (detached, no authenticated
// attributes), digest SHA-256. Written as DER by hand: the standard library has no PKCS #7.
func pkcs7(s *Signer, sf []byte) ([]byte, error) {
	sig, err := s.sign(sf)
	if err != nil {
		return nil, err
	}
	sha256Alg := der(0x30, oid(2, 16, 840, 1, 101, 3, 4, 2, 1), der(0x05))
	var sigAlg []byte
	if _, ok := s.key.(*ecdsa.PrivateKey); ok {
		sigAlg = der(0x30, oid(1, 2, 840, 10045, 4, 3, 2)) // ecdsa-with-SHA256
	} else {
		sigAlg = der(0x30, oid(1, 2, 840, 113549, 1, 1, 1), der(0x05)) // rsaEncryption
	}
	signerInfo := der(0x30,
		derInt(big.NewInt(1)),
		der(0x30, s.cert.RawIssuer, derInt(s.cert.SerialNumber)),
		sha256Alg,
		sigAlg,
		der(0x04, sig),
	)
	signedData := der(0x30,
		derInt(big.NewInt(1)),
		der(0x31, sha256Alg),
		der(0x30, oid(1, 2, 840, 113549, 1, 7, 1)),
		der(0xa0, s.certDER),
		der(0x31, signerInfo),
	)
	return der(0x30, oid(1, 2, 840, 113549, 1, 7, 2), der(0xa0, signedData)), nil
}

// der is one DER element: tag, length, contents.
func der(tag byte, contents ...[]byte) []byte {
	n := 0
	for _, c := range contents {
		n += len(c)
	}
	out := []byte{tag}
	switch {
	case n < 0x80:
		out = append(out, byte(n))
	case n < 0x100:
		out = append(out, 0x81, byte(n))
	case n < 0x10000:
		out = append(out, 0x82, byte(n>>8), byte(n))
	case n < 0x1000000:
		out = append(out, 0x83, byte(n>>16), byte(n>>8), byte(n))
	default:
		out = append(out, 0x84, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	for _, c := range contents {
		out = append(out, c...)
	}
	return out
}

// oid is an OBJECT IDENTIFIER.
func oid(arcs ...int) []byte {
	body := []byte{byte(arcs[0]*40 + arcs[1])}
	for _, a := range arcs[2:] {
		var tmp []byte
		tmp = append(tmp, byte(a&0x7f))
		for a >>= 7; a > 0; a >>= 7 {
			tmp = append(tmp, byte(0x80|a&0x7f))
		}
		for i := len(tmp) - 1; i >= 0; i-- {
			body = append(body, tmp[i])
		}
	}
	return der(0x06, body)
}

// derInt is a non-negative INTEGER.
func derInt(v *big.Int) []byte {
	b := v.Bytes()
	if len(b) == 0 || b[0]&0x80 != 0 {
		b = append([]byte{0}, b...)
	}
	return der(0x02, b)
}
