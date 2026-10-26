package vlatte

import "testing"

func TestHashFuncs(t *testing.T) {
	runFuncCases(t, []funcCase{
		{name: "sha256 in hex", text: `{{ hash "sha256" "hex" "abc" }}`, want: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{name: "sha256 in base64", text: `{{ "abc" | hash "sha256" "base64" }}`, want: "ungWv48Bz+pBQUDeXa4iI7ADYaOWF3qctBD/YfIAFa0="},
		{name: "sha3-256", text: `{{ hash "sha3-256" "hex" "abc" }}`, want: "3a985da74fe225b2045c172d6bd390bd855f086e3e9d525b46bfe24511431532"},
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
