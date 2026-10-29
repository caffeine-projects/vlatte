// Package jose signs and verifies JSON Web Signatures (RFC 7515) in compact form, and converts public keys to and
// from JSON Web Keys (RFC 7517), for the asymmetric algorithms of RFC 7518 and RFC 8037. HMAC algorithms and "none"
// are refused: a provider never publishes the secret of an HMAC key, so a mock signing with one teaches clients to
// accept what no real provider sends. It is where vlatte's jwt and jwks template functions and its JWT matchers do
// their cryptography.
package jose

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	json "encoding/json/v2"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strings"
)

// _minRSABits is the smallest RSA key the package signs or verifies with, as RFC 7518 §3.3 requires.
const _minRSABits = 2048

var (
	// ErrUnsupportedAlg says an algorithm is not one the package signs or verifies with.
	ErrUnsupportedAlg = errors.New("jose: unsupported algorithm")
	// ErrKeyMismatch says a key is not of the type, size or curve its algorithm takes.
	ErrKeyMismatch = errors.New("jose: key does not fit the algorithm")
	// ErrSignature says a signature does not verify.
	ErrSignature = errors.New("jose: signature does not verify")
)

type family int

const (
	_familyRSA family = iota
	_familyPSS
	_familyECDSA
	_familyEdDSA
)

// algorithm is how a JWS algorithm signs: its family, its hash and, for ECDSA, its curve.
type algorithm struct {
	family family
	hash   crypto.Hash
	curve  elliptic.Curve
}

// _algorithms are the algorithms the package signs and verifies with, by their JWS name.
var _algorithms = map[string]algorithm{
	"RS256": {family: _familyRSA, hash: crypto.SHA256},
	"RS384": {family: _familyRSA, hash: crypto.SHA384},
	"RS512": {family: _familyRSA, hash: crypto.SHA512},
	"PS256": {family: _familyPSS, hash: crypto.SHA256},
	"PS384": {family: _familyPSS, hash: crypto.SHA384},
	"PS512": {family: _familyPSS, hash: crypto.SHA512},
	"ES256": {family: _familyECDSA, hash: crypto.SHA256, curve: elliptic.P256()},
	"ES384": {family: _familyECDSA, hash: crypto.SHA384, curve: elliptic.P384()},
	"ES512": {family: _familyECDSA, hash: crypto.SHA512, curve: elliptic.P521()},
	"EdDSA": {family: _familyEdDSA},
}

// Algorithms returns the names of the algorithms the package signs and verifies with, sorted.
func Algorithms() []string {
	return slices.Sorted(maps.Keys(_algorithms))
}

// CheckAlg returns an error wrapping [ErrUnsupportedAlg] unless the package signs and verifies with alg.
func CheckAlg(alg string) error {
	_, err := lookup(alg)
	return err
}

// lookup returns the algorithm named alg, or an error that says why the package doesn't take it.
func lookup(alg string) (algorithm, error) {
	if a, ok := _algorithms[alg]; ok {
		return a, nil
	}

	switch {
	case strings.HasPrefix(alg, "HS"):
		return algorithm{}, fmt.Errorf("%w %q: HMAC signs with a shared secret no provider publishes; use one of %s",
			ErrUnsupportedAlg, alg, strings.Join(Algorithms(), ", "))
	case strings.EqualFold(alg, "none"):
		return algorithm{}, fmt.Errorf("%w %q: an unsigned token proves nothing", ErrUnsupportedAlg, alg)
	default:
		return algorithm{}, fmt.Errorf("%w %q: use one of %s", ErrUnsupportedAlg, alg,
			strings.Join(Algorithms(), ", "))
	}
}

// CheckKey returns an error wrapping [ErrUnsupportedAlg] or [ErrKeyMismatch] unless key can sign with alg: an RSA
// key of 2048 bits or more for RS* and PS*, an ECDSA key on the algorithm's curve for ES*, an Ed25519 key for EdDSA.
func CheckKey(alg string, key crypto.Signer) error {
	if key == nil {
		return fmt.Errorf("%w: no key", ErrKeyMismatch)
	}

	return CheckPublicKey(alg, key.Public())
}

// CheckPublicKey is [CheckKey] for a public key.
func CheckPublicKey(alg string, pub crypto.PublicKey) error {
	a, err := lookup(alg)
	if err != nil {
		return err
	}

	switch a.family {
	case _familyRSA, _familyPSS:
		k, ok := pub.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: %s takes an RSA key, not %s", ErrKeyMismatch, alg, keyType(pub))
		}

		if bits := k.N.BitLen(); bits < _minRSABits {
			return fmt.Errorf("%w: %s takes an RSA key of %d bits or more, not %d", ErrKeyMismatch, alg, _minRSABits, bits)
		}
	case _familyECDSA:
		k, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: %s takes an ECDSA key, not %s", ErrKeyMismatch, alg, keyType(pub))
		}

		if k.Curve != a.curve {
			return fmt.Errorf("%w: %s takes a key on %s, not %s", ErrKeyMismatch, alg, a.curve.Params().Name,
				k.Curve.Params().Name)
		}
	case _familyEdDSA:
		if _, ok := pub.(ed25519.PublicKey); !ok {
			return fmt.Errorf("%w: %s takes an Ed25519 key, not %s", ErrKeyMismatch, alg, keyType(pub))
		}
	}

	return nil
}

// Sign returns a compact JWS of payload, with the header {"alg":alg,"kid":kid,"typ":"JWT"}, signed with key. An
// empty kid is left out of the header. RS* signatures are deterministic: the same payload signs the same way.
func Sign(alg, kid string, key crypto.Signer, payload []byte) (string, error) {
	if err := CheckKey(alg, key); err != nil {
		return "", err
	}

	header, err := json.Marshal(struct {
		Alg string `json:"alg"`
		Kid string `json:"kid,omitempty"`
		Typ string `json:"typ"`
	}{Alg: alg, Kid: kid, Typ: "JWT"})
	if err != nil {
		return "", fmt.Errorf("jose: header: %w", err)
	}

	input := encode(header) + "." + encode(payload)

	sig, err := sign(_algorithms[alg], key, []byte(input))
	if err != nil {
		return "", fmt.Errorf("jose: sign: %w", err)
	}

	return input + "." + encode(sig), nil
}

// sign signs input with key, which CheckKey found fits a.
func sign(a algorithm, key crypto.Signer, input []byte) ([]byte, error) {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		if a.family == _familyPSS {
			return rsa.SignPSS(rand.Reader, k, a.hash, digest(a.hash, input),
				&rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		}

		return rsa.SignPKCS1v15(nil, k, a.hash, digest(a.hash, input))
	case *ecdsa.PrivateKey:
		// JWS writes an ECDSA signature as R and S, each the size of the curve, not as ASN.1 (RFC 7518 §3.4).
		r, s, err := ecdsa.Sign(rand.Reader, k, digest(a.hash, input))
		if err != nil {
			return nil, err
		}

		size := curveBytes(a.curve)
		sig := make([]byte, 2*size)
		r.FillBytes(sig[:size])
		s.FillBytes(sig[size:])

		return sig, nil
	case ed25519.PrivateKey:
		return ed25519.Sign(k, input), nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrKeyMismatch, keyType(key))
	}
}

// verify returns [ErrSignature] unless sig is the signature of input with alg by the key whose public half is pub.
// pub must fit alg: see [CheckPublicKey].
func verify(alg string, pub crypto.PublicKey, input, sig []byte) error {
	a := _algorithms[alg]

	var ok bool

	switch k := pub.(type) {
	case *rsa.PublicKey:
		if a.family == _familyPSS {
			ok = rsa.VerifyPSS(k, a.hash, digest(a.hash, input), sig,
				&rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}) == nil
		} else {
			ok = rsa.VerifyPKCS1v15(k, a.hash, digest(a.hash, input), sig) == nil
		}
	case *ecdsa.PublicKey:
		size := curveBytes(a.curve)
		if len(sig) == 2*size {
			r := new(big.Int).SetBytes(sig[:size])
			s := new(big.Int).SetBytes(sig[size:])
			ok = ecdsa.Verify(k, digest(a.hash, input), r, s)
		}
	case ed25519.PublicKey:
		ok = ed25519.Verify(k, input, sig)
	}

	if !ok {
		return ErrSignature
	}

	return nil
}

// GenerateKey returns a new key for alg: RSA keys are 2048 bits.
func GenerateKey(alg string) (crypto.Signer, error) {
	a, err := lookup(alg)
	if err != nil {
		return nil, err
	}

	switch a.family {
	case _familyRSA, _familyPSS:
		return rsa.GenerateKey(rand.Reader, _minRSABits)
	case _familyECDSA:
		return ecdsa.GenerateKey(a.curve, rand.Reader)
	default:
		_, key, err := ed25519.GenerateKey(nil)
		return key, err
	}
}

// ParsePrivateKey returns the private key of the first PEM block of data: PKCS #1 ("RSA PRIVATE KEY"), PKCS #8
// ("PRIVATE KEY") or SEC 1 ("EC PRIVATE KEY"). Errors never hold key material.
func ParsePrivateKey(data []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("jose: no PEM block")
	}

	var (
		key any
		err error
	)

	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("jose: PEM block %q is not a private key", block.Type)
	}

	if err != nil {
		return nil, fmt.Errorf("jose: PEM block %q: %w", block.Type, err)
	}

	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("jose: PEM block %q holds %s, which can't sign", block.Type, keyType(key))
	}

	return signer, nil
}

func digest(h crypto.Hash, input []byte) []byte {
	d := h.New()
	d.Write(input)

	return d.Sum(nil)
}

func curveBytes(c elliptic.Curve) int {
	return (c.Params().BitSize + 7) / 8
}

func keyType(key any) string {
	switch key.(type) {
	case *rsa.PublicKey, *rsa.PrivateKey:
		return "an RSA key"
	case *ecdsa.PublicKey, *ecdsa.PrivateKey:
		return "an ECDSA key"
	case ed25519.PublicKey, ed25519.PrivateKey:
		return "an Ed25519 key"
	default:
		return fmt.Sprintf("a %T", key)
	}
}

func encode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
