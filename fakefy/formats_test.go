package fakefy_test

import (
	"testing"
	"uuid"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/fakefy"
)

func TestSchema_FormatsMakeStringsTheValidatorAccepts(t *testing.T) {
	formats := []string{
		"date-time", "date", "time", "duration", "email", "idn-email", "hostname", "idn-hostname", "ipv4", "ipv6",
		"uri", "iri", "uri-reference", "iri-reference", "uri-template", "json-pointer", "relative-json-pointer",
		"uuid", "regex",
	}

	for _, format := range formats {
		t.Run(format, func(t *testing.T) {
			sch, doc := compile(t, `{"type": "string", "format": "`+format+`"}`)

			for seed := range uint64(_seeds) {
				v, err := fakefy.New(fakefy.WithSeed(seed)).Schema(doc)
				require.NoError(t, err)
				requireValid(t, sch, v)
			}
		})
	}
}

func TestSchema_AUUIDIsRandomVersion4(t *testing.T) {
	v, err := fakefy.New(fakefy.WithSeed(3)).Schema(map[string]any{"type": "string", "format": "uuid"})
	require.NoError(t, err)

	s, ok := v.(string)
	require.True(t, ok)

	id, err := uuid.Parse(s)
	require.NoError(t, err)
	require.Equal(t, byte(0x40), id[6]&0xf0, "version 4")
	require.Equal(t, byte(0x80), id[8]&0xc0, "variant 10")
}

func TestSchema_TheOpenAPIByteFormatIsBase64(t *testing.T) {
	sch, doc := compile(t, `{"type": "string", "contentEncoding": "base64", "format": "byte"}`)

	v, err := fakefy.New(fakefy.WithSeed(1)).Schema(doc)
	require.NoError(t, err)
	requireValid(t, sch, v)
	require.Regexp(t, `^[A-Za-z0-9+/]+=*$`, v)
}
