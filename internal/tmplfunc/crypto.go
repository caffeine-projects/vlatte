package tmplfunc

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha3"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
)

// _hashes are the algorithms of hash, by name.
var _hashes = map[string]func() hash.Hash{
	"md5":      md5.New,
	"sha1":     sha1.New,
	"sha224":   sha256.New224,
	"sha256":   sha256.New,
	"sha384":   sha512.New384,
	"sha512":   sha512.New,
	"sha3-224": func() hash.Hash { return sha3.New224() },
	"sha3-256": func() hash.Hash { return sha3.New256() },
	"sha3-384": func() hash.Hash { return sha3.New384() },
	"sha3-512": func() hash.Hash { return sha3.New512() },
}

// _hmacHashes are the algorithms of hmac, by name.
var _hmacHashes = map[string]func() hash.Hash{
	"sha1":   sha1.New,
	"sha256": sha256.New,
	"sha512": sha512.New,
}

// hashOf returns the digest of s with algorithm alg, written in encoding: hex, base64 or base64url.
func hashOf(alg, encoding, s string) (string, error) {
	newHash, ok := _hashes[alg]
	if !ok {
		return "", fmt.Errorf("unknown algorithm %q", alg)
	}

	h := newHash()
	h.Write([]byte(s))

	return encodeSum(encoding, h.Sum(nil))
}

// hmacOf returns the HMAC of s with key and algorithm alg, written in encoding: hex, base64 or base64url.
func hmacOf(alg, key, encoding, s string) (string, error) {
	newHash, ok := _hmacHashes[alg]
	if !ok {
		return "", fmt.Errorf("unknown algorithm %q", alg)
	}

	h := hmac.New(newHash, []byte(key))
	h.Write([]byte(s))

	return encodeSum(encoding, h.Sum(nil))
}

func encodeSum(encoding string, sum []byte) (string, error) {
	switch encoding {
	case "hex":
		return hex.EncodeToString(sum), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(sum), nil
	case "base64url":
		// RFC 4648 §5 without padding, as JWS and PKCE (RFC 7636) write it.
		return base64.RawURLEncoding.EncodeToString(sum), nil
	default:
		return "", fmt.Errorf("unknown encoding %q", encoding)
	}
}
