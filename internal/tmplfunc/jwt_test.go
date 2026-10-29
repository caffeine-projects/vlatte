package tmplfunc

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	json "encoding/json/v2"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/jose"
)

// keysFuncs returns Funcs with the jwt and jwks of k.
func keysFuncs(k *Keys) map[string]any {
	funcs := Funcs()
	funcs["jwt"] = k.JWT
	funcs["jwks"] = k.JWKS

	return funcs
}

func TestKeys_SignsTokensItsJWKSVerifies(t *testing.T) {
	// A client that reads the server's JWKS, as a real one does, must accept the tokens the server's templates sign.
	k := testKeys(t)
	funcs := keysFuncs(k)

	jwks, err := render(t, funcs, `{{ jwks "es-1" "ed-1" }}`, nil)
	require.NoError(t, err)

	ks, err := jose.ParseKeySet([]byte(jwks))
	require.NoError(t, err)

	tests := []struct {
		name string
		text string
		data any
		kid  string
		alg  string
	}{
		{name: "claims as JSON text", text: `{{ jwt "es-1" "{\"sub\": \"ana\", \"admin\": true}" }}`, kid: "es-1", alg: "ES256"},
		{name: "claims piped", text: `{{ "{\"sub\":\"ana\",\"admin\":true}" | jwt "ed-1" }}`, kid: "ed-1", alg: "EdDSA"},
		{name: "claims as fromJSON returns them", text: `{{ fromJSON .body | jwt "es-1" }}`, data: map[string]any{"body": `{"admin":true,"sub":"ana"}`}, kid: "es-1", alg: "ES256"},
		{name: "a kid read from data", text: `{{ jwt .kid "{\"sub\":\"ana\",\"admin\":true}" }}`, data: map[string]any{"kid": "ed-1"}, kid: "ed-1", alg: "EdDSA"},
		{name: "claims as bytes", text: `{{ jwt "es-1" .body }}`, data: map[string]any{"body": []byte(`{"sub": "ana", "admin": true}`)}, kid: "es-1", alg: "ES256"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := render(t, funcs, tt.text, tt.data)
			require.NoError(t, err)

			tok, err := jose.Parse(token)
			require.NoError(t, err)
			require.Equal(t, map[string]any{"alg": tt.alg, "kid": tt.kid, "typ": "JWT"}, tok.Header)
			require.Equal(t, map[string]any{"sub": "ana", "admin": true}, tok.Claims) // and no claim added
			require.NoError(t, ks.Verify(tok))
		})
	}
}

func TestKeys_JWKSPublishesTheKeysNamedInOrder(t *testing.T) {
	// Publishing a chosen set is how a mock rotates keys: the old and the new one, then the new one alone.
	funcs := keysFuncs(testKeys(t))

	out, err := render(t, funcs, `{{ $s := fromJSON (jwks "ed-1" "es-1") }}{{ range $s.keys }}{{ .kid }}:{{ .kty }}:{{ .alg }} {{ end }}`, nil)
	require.NoError(t, err)
	require.Equal(t, "ed-1:OKP:EdDSA es-1:EC:ES256 ", out)

	out, err = render(t, funcs, `{{ jwks "es-1" }}`, nil)
	require.NoError(t, err)
	require.NotContains(t, out, `"d"`) // never the private part
}

func TestKeys_RefusesWhatItCantSign(t *testing.T) {
	funcs := keysFuncs(testKeys(t))

	tests := []struct {
		name string
		text string
		err  string
	}{
		{name: "an unknown kid", text: `{{ jwt "es-9" "{}" }}`, err: `error calling jwt: "es-9" is not a signing key of the server`},
		{name: "an unknown kid in jwks", text: `{{ jwks "es-1" "es-9" }}`, err: `error calling jwks: "es-9" is not a signing key of the server`},
		{name: "jwks of no key", text: `{{ jwks }}`, err: "error calling jwks: name at least one key"},
		{name: "claims that aren't JSON", text: `{{ jwt "es-1" "sub=ana" }}`, err: "error calling jwt: claims are not JSON: "},
		{name: "claims that aren't an object", text: `{{ jwt "es-1" "[1, 2]" }}`, err: "error calling jwt: claims are not a JSON object"},
		{name: "a claim given twice", text: `{{ jwt "es-1" "{\"sub\":\"a\",\"sub\":\"b\"}" }}`, err: "error calling jwt: claims are not JSON: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := render(t, funcs, tt.text, nil)
			require.ErrorContains(t, err, tt.err)
		})
	}
}

func TestKeys_Add(t *testing.T) {
	ec := testKeys(t).keys["es-1"].key

	var k Keys
	require.NoError(t, k.Add("a", "ES256", ec))

	tests := []struct {
		name string
		kid  string
		alg  string
		key  crypto.Signer
		msg  string
	}{
		{name: "an empty kid", alg: "ES256", key: ec, msg: "signing key: kid is empty"},
		{name: "a kid added twice", kid: "a", alg: "ES256", key: ec, msg: `signing key "a" is added twice`},
		{
			name: "HMAC", kid: "b", alg: "HS256", key: ec,
			msg: `signing key "b": jose: unsupported algorithm "HS256": HMAC signs with a shared secret no provider publishes: use one of ES256, ES384, ES512, EdDSA, PS256, PS384, PS512, RS256, RS384, RS512`,
		},
		{name: "a key that doesn't fit", kid: "b", alg: "RS256", key: ec, msg: `signing key "b": jose: key does not fit the algorithm: RS256 takes an RSA key, not an ECDSA key`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.EqualError(t, k.Add(tt.kid, tt.alg, tt.key), tt.msg)
		})
	}
}

func TestKeys_RefusesClaimsJSONCantHold(t *testing.T) {
	// The json package words this error one of two ways, at random once per process: the test reads its type.
	_, err := render(t, keysFuncs(testKeys(t)), `{{ jwt "es-1" .claims }}`, map[string]any{"claims": map[string]any{"ch": make(chan int)}})
	require.ErrorContains(t, err, "error calling jwt: claims: json: ")

	semantic, ok := errors.AsType[*json.SemanticError](err)
	require.True(t, ok, err)
	require.Equal(t, reflect.TypeFor[chan int](), semantic.GoType)
}

func TestKeys_Check(t *testing.T) {
	// A mock that names a key the server lacks fails as it loads, not on the request that renders it.
	k := testKeys(t)

	tests := []struct {
		name string
		text string
		err  string
	}{
		{name: "known kids", text: `{{ jwt "es-1" .claims }} {{ .claims | jwt "ed-1" }} {{ jwks "es-1" "ed-1" }}`},
		{name: "an unknown kid of jwt", text: `{{ jwt "es-9" .claims }}`, err: `"es-9" is not a signing key of the server`},
		{name: "an unknown kid piped claims", text: `{{ .claims | jwt "es-9" }}`, err: `"es-9" is not a signing key of the server`},
		{name: "an unknown kid of jwks", text: `{{ if true }}{{ jwks "es-1" "es-9" }}{{ end }}`, err: `"es-9" is not a signing key of the server`},
		{name: "claims given as a literal aren't a kid", text: `{{ jwt "es-1" "{}" }}`},
		{name: "a kid read from data is checked when it renders", text: `{{ jwt .kid .claims }}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl := template.Must(template.New("t").Funcs(keysFuncs(k)).Parse(tt.text))

			err := k.Check(tmpl)
			if tt.err == "" {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, ErrUnknownKey)
			require.EqualError(t, err, tt.err)
		})
	}
}

func TestParseKeySpec(t *testing.T) {
	tests := []struct {
		spec string
		want KeySpec
		msg  string
	}{
		{spec: "google-1=RS256", want: KeySpec{Kid: "google-1", Alg: "RS256"}},
		{spec: "apple-1=ES256:keys/apple.pem", want: KeySpec{Kid: "apple-1", Alg: "ES256", Path: "keys/apple.pem"}},
		{spec: `win=EdDSA:C:\keys\ed.pem`, want: KeySpec{Kid: "win", Alg: "EdDSA", Path: `C:\keys\ed.pem`}},
		{spec: "google-1", msg: `signing key "google-1": write it as kid=ALG or kid=ALG:path/to/key.pem`},
		{spec: "=RS256", msg: `signing key "=RS256": write it as kid=ALG or kid=ALG:path/to/key.pem`},
		{spec: "a=RS256:", msg: `signing key "a": the path after "RS256:" is empty`},
		{spec: "a=HS256", msg: `signing key "a": jose: unsupported algorithm "HS256": HMAC signs with a shared secret no provider publishes: use one of ES256, ES384, ES512, EdDSA, PS256, PS384, PS512, RS256, RS384, RS512`},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			got, err := ParseKeySpec(tt.spec)
			if tt.msg != "" {
				require.EqualError(t, err, tt.msg)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestKeySpec_Load(t *testing.T) {
	dir := t.TempDir()
	resolve := func(p string) string { return filepath.Join(dir, p) }

	ec := testKeys(t).keys["es-1"].key
	der, err := x509.MarshalPKCS8PrivateKey(ec)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ec.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600))

	t.Run("generates a key without a path", func(t *testing.T) {
		key, err := KeySpec{Kid: "a", Alg: "ES384"}.Load(resolve)
		require.NoError(t, err)
		require.NoError(t, jose.CheckKey("ES384", key))
	})

	t.Run("reads the key of a path, resolved", func(t *testing.T) {
		key, err := KeySpec{Kid: "a", Alg: "ES256", Path: "ec.pem"}.Load(resolve)
		require.NoError(t, err)
		require.True(t, ec.Public().(*ecdsa.PublicKey).Equal(key.Public()))
	})

	t.Run("refuses a key that doesn't fit the algorithm", func(t *testing.T) {
		_, err := KeySpec{Kid: "a", Alg: "RS256", Path: "ec.pem"}.Load(resolve)
		require.EqualError(t, err, `signing key "a": ec.pem: jose: key does not fit the algorithm: RS256 takes an RSA key, not an ECDSA key`)
	})

	t.Run("refuses an algorithm it can't generate a key for", func(t *testing.T) {
		_, err := KeySpec{Kid: "a", Alg: "HS256"}.Load(resolve)
		require.ErrorIs(t, err, jose.ErrUnsupportedAlg)
		require.EqualError(t, err, `signing key "a": jose: unsupported algorithm "HS256": HMAC signs with a shared secret no provider publishes: use one of ES256, ES384, ES512, EdDSA, PS256, PS384, PS512, RS256, RS384, RS512`)
	})

	t.Run("names the file that holds no key", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a key"), 0o600))

		_, err := KeySpec{Kid: "a", Alg: "ES256", Path: "notes.txt"}.Load(resolve)
		require.EqualError(t, err, `signing key "a": notes.txt: jose: no PEM block`)
	})

	t.Run("a missing file names the key", func(t *testing.T) {
		_, err := KeySpec{Kid: "a", Alg: "RS256", Path: "nope.pem"}.Load(resolve)
		require.ErrorIs(t, err, os.ErrNotExist)
		require.ErrorContains(t, err, `signing key "a": `)
	})
}

func TestLoadKeySpecs_LoadsEachKeyInOrder(t *testing.T) {
	keys, err := LoadKeySpecs([]string{"b=EdDSA", "a=ES256"}, func(p string) string { return p })
	require.NoError(t, err)
	require.Len(t, keys, 2)
	require.Equal(t, []string{"b", "a"}, []string{keys[0].Kid, keys[1].Kid})
	require.Equal(t, []string{"EdDSA", "ES256"}, []string{keys[0].Alg, keys[1].Alg})
	require.NoError(t, jose.CheckKey("EdDSA", keys[0].Key))
	require.NoError(t, jose.CheckKey("ES256", keys[1].Key))
}

func TestLoadKeySpecs_RefusesASpecItCantParse(t *testing.T) {
	// A setting with one bad spec loads no key, rather than a server missing the key its mocks name.
	keys, err := LoadKeySpecs([]string{"a=ES256", "b"}, func(p string) string { return p })
	require.EqualError(t, err, `signing key "b": write it as kid=ALG or kid=ALG:path/to/key.pem`)
	require.Nil(t, keys)
}

// Generators

// testKeys returns Keys with an ES256 key as es-1 and an EdDSA key as ed-1: quick to make, unlike RSA ones, which
// the jose package's tests cover.
func testKeys(t *testing.T) *Keys {
	t.Helper()

	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	ed, err := jose.GenerateKey("EdDSA")
	require.NoError(t, err)

	var k Keys
	require.NoError(t, k.Add("es-1", "ES256", ec))
	require.NoError(t, k.Add("ed-1", "EdDSA", ed))

	return &k
}
