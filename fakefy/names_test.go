package fakefy_test

import (
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/fakefy"
)

var _str = map[string]any{"type": "string"}

func TestField_TheNameMakesAStringThatLooksLikeIt(t *testing.T) {
	// A mock's body should read like the real API's: an email field with "garden river" in it reads as broken.
	tests := []struct {
		names []string
		check func(t *testing.T, v string)
	}{
		{names: []string{"email", "contactEmail", "email_address"}, check: func(t *testing.T, v string) {
			_, err := mail.ParseAddress(v)
			require.NoError(t, err)
		}},
		{names: []string{"createdAt", "updated_at", "birthDate", "timestamp"}, check: func(t *testing.T, v string) {
			_, err := time.Parse(time.RFC3339, v)
			require.NoError(t, err)
		}},
		{names: []string{"id", "petId", "owner_id", "ID"}, check: func(t *testing.T, v string) {
			_, err := uuid.Parse(v)
			require.NoError(t, err)
		}},
		{names: []string{"url", "avatarUrl", "website", "photo"}, check: func(t *testing.T, v string) {
			u, err := url.Parse(v)
			require.NoError(t, err)
			require.Equal(t, "https", u.Scheme)
		}},
		{names: []string{"phone", "mobilePhone"}, check: func(t *testing.T, v string) {
			require.Regexp(t, `^\+1-555-01\d\d$`, v)
		}},
		{names: []string{"price", "amount"}, check: func(t *testing.T, v string) {
			require.Regexp(t, `^\d+\.\d\d$`, v)
		}},
		{names: []string{"currency"}, check: func(t *testing.T, v string) {
			require.Regexp(t, `^[A-Z]{3}$`, v)
		}},
		{names: []string{"zip", "postalCode"}, check: func(t *testing.T, v string) {
			require.Regexp(t, `^\d{5}$`, v)
		}},
		{names: []string{"firstName", "name"}, check: func(t *testing.T, v string) {
			require.Regexp(t, `^[A-Z][a-z]+$`, v)
		}},
		{names: []string{"fullName"}, check: func(t *testing.T, v string) {
			require.Regexp(t, `^[A-Z][a-z]+ [A-Z][a-z]+$`, v)
		}},
		{names: []string{"description", "bio"}, check: func(t *testing.T, v string) {
			require.Regexp(t, `^[A-Z][a-z]+( [a-z]+){4,}\.$`, v)
		}},
	}

	for _, tt := range tests {
		for _, name := range tt.names {
			t.Run(name, func(t *testing.T) {
				for seed := range uint64(_seeds) {
					v, err := fakefy.New(fakefy.WithSeed(seed)).Field(name, _str)
					require.NoError(t, err)

					s, ok := v.(string)
					require.True(t, ok)
					tt.check(t, s)
				}
			})
		}
	}
}

func TestField_AnUnknownNameMakesWords(t *testing.T) {
	words := regexp.MustCompile(`^[a-z]+( [a-z]+)?$`)

	for seed := range uint64(_seeds) {
		v, err := fakefy.New(fakefy.WithSeed(seed)).Field("breed", _str)
		require.NoError(t, err)
		require.Regexp(t, words, v)
	}
}

func TestField_TheNamePicksTheRangeOfANumberWithoutBounds(t *testing.T) {
	tests := []struct {
		name   string
		schema map[string]any
		lo     float64
		hi     float64
	}{
		{name: "age", schema: map[string]any{"type": "integer"}, lo: 1, hi: 90},
		{name: "year", schema: map[string]any{"type": "integer"}, lo: 2000, hi: 2026},
		{name: "latitude", schema: map[string]any{"type": "number"}, lo: -90, hi: 90},
		{name: "rating", schema: map[string]any{"type": "number"}, lo: 1, hi: 5},
		{name: "unknown", schema: map[string]any{"type": "integer"}, lo: 1, hi: 100},
		{name: "age", schema: map[string]any{"type": "integer", "minimum": 200}, lo: 200, hi: 289},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for seed := range uint64(_seeds) {
				v, err := fakefy.New(fakefy.WithSeed(seed)).Field(tt.name, tt.schema)
				require.NoError(t, err)

				n, err := strconv.ParseFloat(stringOf(v), 64)
				require.NoError(t, err)
				require.GreaterOrEqual(t, n, tt.lo)
				require.LessOrEqual(t, n, tt.hi)
			}
		})
	}
}

func TestField_AnIntegerIsAnInt64(t *testing.T) {
	v, err := fakefy.New(fakefy.WithSeed(1)).Field("count", map[string]any{"type": "integer"})
	require.NoError(t, err)
	require.IsType(t, int64(0), v)
}

// stringOf writes a number fakefy made as text.
func stringOf(v any) string {
	switch n := v.(type) {
	case int64:
		return strconv.FormatInt(n, 10)
	case float64:
		return strconv.FormatFloat(n, 'g', -1, 64)
	default:
		return ""
	}
}
