package tmplfunc

import "testing"

func TestHashFuncs(t *testing.T) {
	runFuncCases(t, []funcCase{
		{name: "sha256 in hex", text: `{{ hash "sha256" "hex" "abc" }}`, want: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{name: "sha256 in base64", text: `{{ "abc" | hash "sha256" "base64" }}`, want: "ungWv48Bz+pBQUDeXa4iI7ADYaOWF3qctBD/YfIAFa0="},
		{
			// RFC 7636 appendix B: the S256 code challenge of a PKCE code verifier.
			name: "sha256 in base64url, a PKCE code challenge", text: `{{ hash "sha256" "base64url" "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk" }}`,
			want: "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
		},
		{name: "hmac sha256 in base64url", text: `{{ hmac "sha256" "key" "base64url" "The quick brown fox jumps over the lazy dog" }}`, want: "97yD9DBThCSxMpjmqm-xQ-9NWaFJRhdZl0edvC0aPNg"},
		{name: "sha3-256", text: `{{ hash "sha3-256" "hex" "abc" }}`, want: "3a985da74fe225b2045c172d6bd390bd855f086e3e9d525b46bfe24511431532"},
		{name: "sha3-224", text: `{{ hash "sha3-224" "hex" "abc" }}`, want: "e642824c3f8cf24ad09234ee7d3c766fc9a3a5168d0c94ad73b46fdf"},
		{name: "sha3-384", text: `{{ hash "sha3-384" "hex" "abc" }}`, want: "ec01498288516fc926459f58e2c6ad8df9b473cb0fc08c2596da7cf0e49be4b298d88cea927ac7f539f1edf228376d25"},
		{
			name: "sha3-512", text: `{{ hash "sha3-512" "hex" "abc" }}`,
			want: "b751850b1a57168a5693cd924b6b096e08f621827444f70d884f5d0240d2712e10e116e9192af3c91a7ec57647e3934057340b4cf408d5a56592f8274eec53f0",
		},
		{name: "md5, for checksums", text: `{{ hash "md5" "hex" "abc" }}`, want: "900150983cd24fb0d6963f7d28e17f72"},
		{name: "an unknown algorithm", text: `{{ hash "md2" "hex" "abc" }}`, err: `error calling hash: unknown algorithm "md2"`},
		{name: "an unknown encoding", text: `{{ hash "sha256" "base32" "abc" }}`, err: `error calling hash: unknown encoding "base32"`},
		{
			// A known HMAC-SHA256, the signature webhook providers send with their payloads.
			name: "hmac sha256", text: `{{ hmac "sha256" "key" "hex" "The quick brown fox jumps over the lazy dog" }}`,
			want: "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8",
		},
		{name: "hmac with an algorithm only hash has", text: `{{ hmac "md5" "key" "hex" "x" }}`, err: `error calling hmac: unknown algorithm "md5"`},
	})
}
