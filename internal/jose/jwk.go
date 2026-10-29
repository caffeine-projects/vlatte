package jose

import (
	"bytes"
	"cmp"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
)

// ErrUnknownKid says no key of a key set has the kid of a token.
var ErrUnknownKid = errors.New("jose: kid not in the key set")

// errUnsupportedKey is returned for a JWK of a type or curve the package doesn't support.
var errUnsupportedKey = errors.New("jose: jwk: unsupported")

// _curves are the JWK names of the ECDSA curves, by curve.
var _curves = map[elliptic.Curve]string{
	elliptic.P256(): "P-256",
	elliptic.P384(): "P-384",
	elliptic.P521(): "P-521",
}

// JWK is the public part of a key, as a JSON Web Key.
type JWK struct {
	Kty string `json:"kty"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	Kid string `json:"kid,omitempty"`
	Crv string `json:"crv,omitempty"`
	N   string `json:"n,omitempty"`
	E   string `json:"e,omitempty"`
	X   string `json:"x,omitempty"`
	Y   string `json:"y,omitempty"`
}

// JWKSet is a JSON Web Key Set.
type JWKSet struct {
	Keys []JWK `json:"keys"`
}

// PublicJWK returns the JWK of pub, the public key of a signing key: use "sig", with alg and kid.
func PublicJWK(alg, kid string, pub crypto.PublicKey) (JWK, error) {
	if err := CheckPublicKey(alg, pub); err != nil {
		return JWK{}, err
	}

	k := JWK{Use: "sig", Alg: alg, Kid: kid}

	switch pub := pub.(type) {
	case *rsa.PublicKey:
		k.Kty = "RSA"
		k.N = encode(pub.N.Bytes())
		k.E = encode(big.NewInt(int64(pub.E)).Bytes())
	case *ecdsa.PublicKey:
		b, err := pub.Bytes() // 0x04, then X and Y, each the size of the curve
		if err != nil {
			return JWK{}, fmt.Errorf("jose: %w", err)
		}

		size := curveBytes(pub.Curve)
		k.Kty = "EC"
		k.Crv = _curves[pub.Curve]
		k.X = encode(b[1 : 1+size])
		k.Y = encode(b[1+size:])
	case ed25519.PublicKey:
		k.Kty = "OKP"
		k.Crv = "Ed25519"
		k.X = encode(pub)
	}

	return k, nil
}

// publicKey returns the public key k describes.
func (k *JWK) publicKey() (crypto.PublicKey, error) {
	switch k.Kty {
	case "RSA":
		n, err := decodeField("n", k.N)
		if err != nil {
			return nil, err
		}

		e, err := decodeField("e", k.E)
		if err != nil {
			return nil, err
		}

		exp := new(big.Int).SetBytes(e)
		if !exp.IsInt64() || exp.Int64() > 1<<31-1 {
			return nil, errors.New("jose: jwk: e is too large")
		}

		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exp.Int64())}, nil
	case "EC":
		var curve elliptic.Curve

		for c, name := range _curves {
			if name == k.Crv {
				curve = c
			}
		}

		if curve == nil {
			return nil, fmt.Errorf("%w curve %q", errUnsupportedKey, k.Crv)
		}

		x, err := decodeField("x", k.X)
		if err != nil {
			return nil, err
		}

		y, err := decodeField("y", k.Y)
		if err != nil {
			return nil, err
		}

		pub, err := ecdsa.ParseUncompressedPublicKey(curve, bytes.Join([][]byte{{4}, x, y}, nil))
		if err != nil {
			return nil, fmt.Errorf("jose: jwk: %w", err)
		}

		return pub, nil
	case "OKP":
		if k.Crv != "Ed25519" {
			return nil, fmt.Errorf("%w curve %q", errUnsupportedKey, k.Crv)
		}

		x, err := decodeField("x", k.X)
		if err != nil {
			return nil, err
		}

		if len(x) != ed25519.PublicKeySize {
			return nil, errors.New("jose: jwk: x is not an Ed25519 public key")
		}

		return ed25519.PublicKey(x), nil
	default:
		return nil, fmt.Errorf("%w kty %q", errUnsupportedKey, k.Kty)
	}
}

func decodeField(name, s string) ([]byte, error) {
	if s == "" {
		return nil, fmt.Errorf("jose: jwk: no %s", name)
	}

	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("jose: jwk: %s: %w", name, err)
	}

	return b, nil
}

// KeySet is the public keys a token may be signed with.
type KeySet struct {
	keys []setKey
}

// setKey is a key of a [KeySet], with the kid and alg its JWK gave, if any.
type setKey struct {
	kid string
	alg string
	pub crypto.PublicKey
}

// ParseKeySet reads the public keys of text: a JWK Set, a single JWK, or PEM blocks of public keys ("PUBLIC KEY",
// "RSA PUBLIC KEY") and certificates ("CERTIFICATE"). JWKs of a set whose use is "enc", or whose type or curve the
// package doesn't support, are skipped; a JWK of a supported type that can't be read is an error. A private key, PEM
// or JWK, is refused, so a mock file can't hold one by mistake. A set with no key is an error.
func ParseKeySet(text []byte) (*KeySet, error) {
	text = bytes.TrimSpace(text)

	var (
		ks  *KeySet
		err error
	)

	switch {
	case bytes.HasPrefix(text, []byte("{")):
		ks, err = parseJWKs(text)
	case bytes.HasPrefix(text, []byte("-----BEGIN")):
		ks, err = parsePEMs(text)
	default:
		return nil, errors.New("jose: a key set is a JWK Set, a JWK or PEM public keys")
	}

	if err != nil {
		return nil, err
	}

	if len(ks.keys) == 0 {
		return nil, errors.New("jose: the key set has no signing key")
	}

	return ks, nil
}

// readJWK is a JWK as ParseKeySet reads it, with d: the private key of an RSA, EC or OKP JWK (RFC 7518 §6.3.2.1 and
// §6.2.2.1, RFC 8037 §2), which a public JWK never has.
type readJWK struct {
	JWK

	D jsontext.Value `json:"d"`
}

func parseJWKs(text []byte) (*KeySet, error) {
	var doc struct {
		readJWK

		Keys []readJWK `json:"keys"`
	}
	if err := json.Unmarshal(text, &doc); err != nil {
		return nil, fmt.Errorf("jose: key set: %w", err)
	}

	// A mock that held a private key would show it through the admin API, as it would a PEM one.
	for _, k := range append(doc.Keys, doc.readJWK) {
		if k.D != nil {
			return nil, fmt.Errorf("jose: jwk %q holds a private key: give its public half", k.Kid)
		}
	}

	if doc.Kty != "" {
		pub, err := doc.publicKey()
		if err != nil {
			return nil, err
		}

		return &KeySet{keys: []setKey{{kid: doc.Kid, alg: doc.Alg, pub: pub}}}, nil
	}

	var (
		ks          = &KeySet{}
		unsupported error
	)

	for _, k := range doc.Keys {
		if k.Use == "enc" {
			continue
		}

		pub, err := k.publicKey()
		if errors.Is(err, errUnsupportedKey) {
			unsupported = cmp.Or(unsupported, err)
			continue
		}

		if err != nil {
			return nil, err
		}

		ks.keys = append(ks.keys, setKey{kid: k.Kid, alg: k.Alg, pub: pub})
	}

	// A set of keys the package can't use says why.
	if len(ks.keys) == 0 && unsupported != nil {
		return nil, unsupported
	}

	return ks, nil
}

func parsePEMs(text []byte) (*KeySet, error) {
	ks := &KeySet{}

	for {
		var block *pem.Block
		if block, text = pem.Decode(text); block == nil {
			break
		}

		var (
			pub any
			err error
		)

		switch block.Type {
		case "PUBLIC KEY":
			pub, err = x509.ParsePKIXPublicKey(block.Bytes)
		case "RSA PUBLIC KEY":
			pub, err = x509.ParsePKCS1PublicKey(block.Bytes)
		case "CERTIFICATE":
			var cert *x509.Certificate
			if cert, err = x509.ParseCertificate(block.Bytes); err == nil {
				pub = cert.PublicKey
			}
		default:
			return nil, fmt.Errorf("jose: PEM block %q is not a public key or a certificate", block.Type)
		}

		if err != nil {
			return nil, fmt.Errorf("jose: PEM block %q: %w", block.Type, err)
		}

		ks.keys = append(ks.keys, setKey{pub: pub})
	}

	return ks, nil
}

// Verify returns nil when a key of ks signed t. It takes only the algorithms of the package, so never "none" or an
// HMAC one. A key whose JWK names a kid is not used for a token that names another kid, and one whose JWK names an alg
// is used only for that alg, so a token can't pick how its key is read. A token without a kid is tried with every key. A token whose kid no key has fails with [ErrUnknownKid]
// unless ks has keys without a kid, such as PEM ones, which are tried in turn.
func (ks *KeySet) Verify(t *Token) error {
	if _, err := lookup(t.Alg); err != nil {
		return err
	}

	var (
		candidates []setKey
		anonymous  []setKey
	)

	for _, k := range ks.keys {
		switch {
		case k.kid == "":
			anonymous = append(anonymous, k)
		case t.Kid == "" || k.kid == t.Kid:
			candidates = append(candidates, k)
		}
	}

	candidates = append(candidates, anonymous...)

	if len(candidates) == 0 {
		return fmt.Errorf("%w: %q", ErrUnknownKid, t.Kid)
	}

	for _, k := range candidates {
		if k.alg != "" && k.alg != t.Alg {
			continue
		}

		if CheckPublicKey(t.Alg, k.pub) != nil {
			continue
		}

		if verify(t.Alg, k.pub, t.input, t.signature) == nil {
			return nil
		}
	}

	return ErrSignature
}
