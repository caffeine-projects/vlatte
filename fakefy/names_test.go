package fakefy_test

import (
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

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
		{names: []string{"lastName", "surname"}, check: func(t *testing.T, v string) {
			require.Regexp(t, `^[A-Z][a-z]+$`, v)
		}},
		{names: []string{"username", "login"}, check: func(t *testing.T, v string) {
			require.Regexp(t, `^[a-z]+\d\d$`, v)
		}},
		{names: []string{"password"}, check: func(t *testing.T, v string) {
			require.Regexp(t, `^[A-Za-z0-9]{12}$`, v)
		}},
		{names: []string{"apiKey", "token"}, check: func(t *testing.T, v string) {
			require.Regexp(t, `^[A-Za-z0-9]{32}$`, v)
		}},
		{names: []string{"city", "country"}, check: func(t *testing.T, v string) {
			require.Regexp(t, `^[A-Z][a-z]+( [A-Z][a-z]+)?$`, v)
		}},
		{names: []string{"countryCode"}, check: func(t *testing.T, v string) {
			require.Regexp(t, `^[A-Z]{2}$`, v)
		}},
		{names: []string{"street", "address"}, check: func(t *testing.T, v string) {
			require.Regexp(t, `^\d{1,3} [A-Z][a-z]+ [A-Z][a-z]+$`, v)
		}},
		{names: []string{"title", "subject"}, check: func(t *testing.T, v string) {
			require.Regexp(t, `^[A-Z][a-z]+( [a-z]+){1,2}$`, v)
		}},
		{names: []string{"company"}, check: func(t *testing.T, v string) {
			require.Regexp(t, `^[A-Z][a-z]+ [A-Z][a-z]+\.?$`, v)
		}},
		{names: []string{"color"}, check: func(t *testing.T, v string) {
			require.Regexp(t, `^[a-z]+$`, v)
		}},
		{names: []string{"locale", "language"}, check: func(t *testing.T, v string) {
			require.Equal(t, "en-US", v)
		}},
		{names: []string{"ipAddress"}, check: func(t *testing.T, v string) {
			ip := net.ParseIP(v)
			require.NotNil(t, ip)
			require.NotNil(t, ip.To4())
		}},
		{names: []string{"hostname", "domain"}, check: func(t *testing.T, v string) {
			require.Regexp(t, `^[a-z]+(\.[a-z]+)+$`, v)
		}},
		{names: []string{"slug"}, check: func(t *testing.T, v string) {
			require.Regexp(t, `^[a-z]+-[a-z]+$`, v)
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
		{name: "month", schema: map[string]any{"type": "integer"}, lo: 1, hi: 12},
		{name: "day", schema: map[string]any{"type": "integer"}, lo: 1, hi: 28},
		{name: "hour", schema: map[string]any{"type": "integer"}, lo: 0, hi: 23},
		{name: "minute", schema: map[string]any{"type": "integer"}, lo: 0, hi: 59},
		{name: "price", schema: map[string]any{"type": "number"}, lo: 1, hi: 500},
		{name: "quantity", schema: map[string]any{"type": "integer"}, lo: 1, hi: 10},
		{name: "pageSize", schema: map[string]any{"type": "integer"}, lo: 10, hi: 50},
		{name: "longitude", schema: map[string]any{"type": "number"}, lo: -180, hi: 180},
		{name: "percent", schema: map[string]any{"type": "number"}, lo: 0, hi: 100},
		{name: "userId", schema: map[string]any{"type": "integer"}, lo: 1, hi: 1000},
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

func TestField_ANameKeepsAStringWithinItsLengths(t *testing.T) {
	// A name only picks what a string looks like: an email that breaks a maxLength fails the contract it mocks.
	rapid.Check(t, func(t *rapid.T) {
		lo := rapid.IntRange(0, 40).Draw(t, "minLength")
		schema := map[string]any{"type": "string", "minLength": lo}
		if rapid.Bool().Draw(t, "bounded") {
			schema["maxLength"] = lo + rapid.IntRange(0, 20).Draw(t, "extra")
		}

		v, err := fakefy.New(fakefy.WithSeed(rapid.Uint64().Draw(t, "seed"))).Field(nameGen().Draw(t, "name"), schema)
		require.NoError(t, err)
		sch, _ := compileMap(t, schema)
		requireValid(t, sch, v)
	})
}

func TestField_ANameKeepsANumberWithinItsBounds(t *testing.T) {
	// A name only picks the range of a number without bounds: an age of 90 under a maximum of 18 fails the contract.
	rapid.Check(t, func(t *rapid.T) {
		lo := rapid.Int64Range(-1000, 1000).Draw(t, "lo")
		schema := map[string]any{"type": rapid.SampledFrom([]string{"integer", "number"}).Draw(t, "type")}

		switch rapid.SampledFrom([]string{"both", "minimum", "maximum", "none"}).Draw(t, "bounds") {
		case "both":
			schema["minimum"], schema["maximum"] = lo, lo+rapid.Int64Range(0, 100).Draw(t, "width")
		case "minimum":
			schema["minimum"] = lo
		case "maximum":
			schema["maximum"] = lo
		}

		v, err := fakefy.New(fakefy.WithSeed(rapid.Uint64().Draw(t, "seed"))).Field(nameGen().Draw(t, "name"), schema)
		require.NoError(t, err)
		sch, _ := compileMap(t, schema)
		requireValid(t, sch, v)
	})
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

// Generators

// nameGen draws field names: ones fakefy knows, in the cases specs write them, and any other string.
func nameGen() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.SampledFrom([]string{
			"email", "contactEmail", "firstName", "fullName", "username", "password", "phone", "avatarUrl", "url", "city",
			"country", "street_address", "zip", "currency", "price", "description", "title", "company", "color",
			"locale", "token", "ip", "hostname", "slug", "createdAt", "birth_date", "id", "petId", "ID", "age", "year",
			"month", "day", "hour", "minute", "quantity", "pageSize", "latitude", "longitude", "rating", "percent",
		}),
		rapid.String(),
	)
}
