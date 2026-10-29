package tmplfunc

import (
	"crypto"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/template"
	"text/template/parse"

	"github.com/caffeine-projects/vlatte/internal/jose"
)

// ErrUnknownKey says a template named a signing key its [Keys] doesn't hold.
var ErrUnknownKey = errors.New("is not a signing key of the server")

// Keys is the jwt and jwks functions of templates: they sign with, and publish the public half of, the keys added to
// it, by kid. The zero Keys holds none. Nothing reads a private key back out of it.
type Keys struct {
	keys map[string]signingKey
}

// signingKey is a key of [Keys] and the algorithm it signs with.
type signingKey struct {
	alg string
	key crypto.Signer
}

// Add adds key as kid, to sign with alg. An empty or repeated kid, an algorithm the jose package refuses, such as
// HS256, or a key that doesn't fit alg is an error, which never holds key material.
func (k *Keys) Add(kid, alg string, key crypto.Signer) error {
	if kid == "" {
		return errors.New("signing key: kid is empty")
	}

	if _, ok := k.keys[kid]; ok {
		return fmt.Errorf("signing key %q is added twice", kid)
	}

	if err := jose.CheckKey(alg, key); err != nil {
		return fmt.Errorf("signing key %q: %w", kid, err)
	}

	if k.keys == nil {
		k.keys = make(map[string]signingKey)
	}

	k.keys[kid] = signingKey{alg: alg, key: key}

	return nil
}

// JWT returns claims signed with the key kid, as a compact JWS whose header has the key's alg, the kid and typ JWT.
// claims is JSON text, such as a template wrote, or a value that marshals to a JSON object, such as fromJSON returns.
// The token holds those claims and no other: no exp or iat is added.
func (k *Keys) JWT(kid string, claims any) (string, error) {
	sk, err := k.key(kid)
	if err != nil {
		return "", err
	}

	payload, err := claimsJSON(claims)
	if err != nil {
		return "", err
	}

	return jose.Sign(sk.alg, kid, sk.key, payload)
}

// JWKS returns the JWK Set of the public halves of the keys kids, in that order, as JSON text.
func (k *Keys) JWKS(kids ...string) (string, error) {
	if len(kids) == 0 {
		return "", errors.New("name at least one key")
	}

	set := jose.JWKSet{Keys: make([]jose.JWK, 0, len(kids))}

	for _, kid := range kids {
		sk, err := k.key(kid)
		if err != nil {
			return "", err
		}

		jwk, err := jose.PublicJWK(sk.alg, kid, sk.key.Public())
		if err != nil {
			return "", err
		}

		set.Keys = append(set.Keys, jwk)
	}

	b, err := json.Marshal(set)

	return string(b), err
}

// Check returns the error jwt or jwks would fail with when t runs, for the first kid given them as a literal that k
// doesn't hold, so a template can fail when it compiles. A kid read from data is checked when it runs.
func (k *Keys) Check(t *template.Template) error {
	for _, tt := range t.Templates() {
		if tt.Tree == nil {
			continue
		}

		var err error

		walkPipes(tt.Root, func(p *parse.PipeNode) bool {
			for _, kid := range keyLiterals(p) {
				if _, err = k.key(kid); err != nil {
					return false
				}
			}

			return true
		})

		if err != nil {
			return err
		}
	}

	return nil
}

func (k *Keys) key(kid string) (signingKey, error) {
	sk, ok := k.keys[kid]
	if !ok {
		return signingKey{}, fmt.Errorf("%q %w", kid, ErrUnknownKey)
	}

	return sk, nil
}

// keyLiterals returns the kids jwt and jwks are given as string literals in a pipeline: the first argument of jwt, as
// in {{ jwt "key-1" $claims }} and {{ $claims | jwt "key-1" }}, and every argument of jwks.
func keyLiterals(p *parse.PipeNode) []string {
	var kids []string

	for _, cmd := range p.Cmds {
		if len(cmd.Args) < 2 {
			continue
		}

		id, ok := cmd.Args[0].(*parse.IdentifierNode)
		if !ok {
			continue
		}

		var args []parse.Node

		switch id.Ident {
		case "jwt":
			args = cmd.Args[1:2]
		case "jwks":
			args = cmd.Args[1:]
		}

		for _, arg := range args {
			if s, ok := arg.(*parse.StringNode); ok {
				kids = append(kids, s.Text)
			}
		}
	}

	return kids
}

// claimsJSON returns claims as compact JSON text, which must be an object.
func claimsJSON(claims any) ([]byte, error) {
	var v jsontext.Value

	switch c := claims.(type) {
	case string:
		v = jsontext.Value(c)
	case []byte:
		v = jsontext.Value(c)
	default:
		b, err := json.Marshal(c, json.Deterministic(true))
		if err != nil {
			return nil, fmt.Errorf("claims: %w", err)
		}

		v = b
	}

	// Compact alone would keep a claim given twice, which verifiers read differently, and invalid UTF-8.
	if err := v.Compact(jsontext.AllowDuplicateNames(false), jsontext.AllowInvalidUTF8(false)); err != nil {
		return nil, fmt.Errorf("claims are not JSON: %w", err)
	}

	if v.Kind() != '{' {
		return nil, errors.New("claims are not a JSON object")
	}

	return v, nil
}

// KeySpec is a signing key as a setting gives it: "kid=ALG", a key generated when it loads, or "kid=ALG:path", one
// read from a PEM file. It holds a path, never key material, so a configuration that shows it holds no secret.
type KeySpec struct {
	Kid  string
	Alg  string
	Path string
}

// ParseKeySpec parses s. An empty kid, a missing or unsupported algorithm, such as HS256, or an empty path after ":"
// is an error.
func ParseKeySpec(s string) (KeySpec, error) {
	kid, rest, ok := strings.Cut(s, "=")
	if !ok || kid == "" || rest == "" {
		return KeySpec{}, fmt.Errorf("signing key %q: write it as kid=ALG or kid=ALG:path/to/key.pem", s)
	}

	alg, path, hasPath := strings.Cut(rest, ":")
	if hasPath && path == "" {
		return KeySpec{}, fmt.Errorf("signing key %q: the path after %q is empty", kid, alg+":")
	}

	if err := jose.CheckAlg(alg); err != nil {
		return KeySpec{}, fmt.Errorf("signing key %q: %w", kid, err)
	}

	return KeySpec{Kid: kid, Alg: alg, Path: path}, nil
}

// Load returns the key of ks: read from Path, which resolve turns into the file to read, or generated without one.
// RSA keys generated are 2048 bits. Errors never hold key material.
func (ks KeySpec) Load(resolve func(path string) string) (crypto.Signer, error) {
	if ks.Path == "" {
		key, err := jose.GenerateKey(ks.Alg)
		if err != nil {
			return nil, fmt.Errorf("signing key %q: %w", ks.Kid, err)
		}

		return key, nil
	}

	data, err := os.ReadFile(resolve(ks.Path))
	if err != nil {
		return nil, fmt.Errorf("signing key %q: %w", ks.Kid, err)
	}

	key, err := jose.ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("signing key %q: %s: %w", ks.Kid, ks.Path, err)
	}

	if err := jose.CheckKey(ks.Alg, key); err != nil {
		return nil, fmt.Errorf("signing key %q: %s: %w", ks.Kid, ks.Path, err)
	}

	return key, nil
}

// SpecKey is a key a [KeySpec] loaded.
type SpecKey struct {
	Kid string
	Alg string
	Key crypto.Signer
}

// CheckKeySpecs returns the error of the first spec [ParseKeySpec] refuses.
func CheckKeySpecs(specs []string) error {
	for _, s := range specs {
		if _, err := ParseKeySpec(s); err != nil {
			return err
		}
	}

	return nil
}

// LoadKeySpecs parses and loads each spec, in order: see [KeySpec.Load].
func LoadKeySpecs(specs []string, resolve func(path string) string) ([]SpecKey, error) {
	keys := make([]SpecKey, 0, len(specs))

	for _, s := range specs {
		ks, err := ParseKeySpec(s)
		if err != nil {
			return nil, err
		}

		key, err := ks.Load(resolve)
		if err != nil {
			return nil, err
		}

		keys = append(keys, SpecKey{Kid: ks.Kid, Alg: ks.Alg, Key: key})
	}

	return keys, nil
}
