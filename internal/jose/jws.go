package jose

import (
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrMalformed says a token is not a compact JWS whose payload is a JSON object.
	ErrMalformed = errors.New("jose: not a JWT")
	// ErrExpired says the exp claim of a token is past.
	ErrExpired = errors.New("jose: token expired")
	// ErrNotYetValid says the nbf claim of a token is future.
	ErrNotYetValid = errors.New("jose: token not yet valid")
)

// Token is a decoded compact JWS whose payload is a JSON object: a JWT. Decoding doesn't verify it.
type Token struct {
	// Header is the decoded protected header.
	Header map[string]any
	// Claims is the decoded payload.
	Claims map[string]any
	// Alg is the alg of the header.
	Alg string
	// Kid is the kid of the header, or "" without one.
	Kid string

	input     []byte // the signing input: header and payload as sent
	signature []byte
}

// Parse decodes a compact JWS. It returns an error wrapping [ErrMalformed] for anything else, such as a JWE, or a
// header without alg.
func Parse(token string) (*Token, error) {
	header, rest, ok := strings.Cut(token, ".")
	payload, sig, ok2 := strings.Cut(rest, ".")

	if !ok || !ok2 || strings.Contains(sig, ".") {
		return nil, fmt.Errorf("%w: a compact JWS has 3 parts", ErrMalformed)
	}

	t := &Token{input: []byte(header + "." + payload)}

	if err := decodeJSON("header", header, &t.Header); err != nil {
		return nil, err
	}

	if err := decodeJSON("payload", payload, &t.Claims); err != nil {
		return nil, err
	}

	alg, ok := t.Header["alg"].(string)
	if !ok || alg == "" {
		return nil, fmt.Errorf("%w: the header has no alg", ErrMalformed)
	}

	t.Alg = alg

	if kid, ok := t.Header["kid"]; ok {
		if t.Kid, ok = kid.(string); !ok {
			return nil, fmt.Errorf("%w: the kid of the header is not a string", ErrMalformed)
		}
	}

	var err error
	if t.signature, err = base64.RawURLEncoding.DecodeString(sig); err != nil {
		return nil, fmt.Errorf("%w: signature: %w", ErrMalformed, err)
	}

	return t, nil
}

func decodeJSON(part, s string, v *map[string]any) error {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrMalformed, part, err)
	}

	if err := json.Unmarshal(b, v); err != nil || *v == nil {
		return fmt.Errorf("%w: the %s is not a JSON object", ErrMalformed, part)
	}

	return nil
}

// CheckTime returns [ErrExpired] when the exp claim of t is now or past, and [ErrNotYetValid] when its nbf claim is
// future, with no leeway. A token without them passes; one whose exp or nbf is not a number is malformed.
func (t *Token) CheckTime(now time.Time) error {
	exp, ok, err := t.numericDate("exp")
	if err != nil {
		return err
	}

	if ok && !now.Before(exp) {
		return fmt.Errorf("%w at %s", ErrExpired, exp.UTC().Format(time.RFC3339))
	}

	nbf, ok, err := t.numericDate("nbf")
	if err != nil {
		return err
	}

	if ok && now.Before(nbf) {
		return fmt.Errorf("%w until %s", ErrNotYetValid, nbf.UTC().Format(time.RFC3339))
	}

	return nil
}

// numericDate returns the claim name of t, a NumericDate: seconds since the epoch.
func (t *Token) numericDate(name string) (time.Time, bool, error) {
	v, ok := t.Claims[name]
	if !ok {
		return time.Time{}, false, nil
	}

	f, ok := v.(float64)
	if !ok {
		return time.Time{}, false, fmt.Errorf("%w: the %s claim is not a number", ErrMalformed, name)
	}

	sec := int64(f)

	return time.Unix(sec, int64((f-float64(sec))*float64(time.Second))), true, nil
}
