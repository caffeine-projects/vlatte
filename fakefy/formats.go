package fakefy

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"
	"uuid"
)

// _epoch is the latest time a date or time can be: values are fixed in time, so a seed makes the same ones on every
// day.
var _epoch = time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)

// format returns a string of the JSON Schema or OpenAPI format name, or "" for a format it doesn't know, such as
// int32, which a string can't have.
func (f *Faker) format(name string) string {
	switch name {
	case "date-time":
		return f.instant().Format(time.RFC3339)
	case "date":
		return f.instant().Format(time.DateOnly)
	case "time":
		return f.instant().Format("15:04:05Z")
	case "duration":
		return fmt.Sprintf("P%dD", 1+f.rnd.IntN(30))
	case "email", "idn-email":
		return f.email()
	case "hostname", "idn-hostname":
		return f.word() + ".example.com"
	case "ipv4":
		// 192.0.2.0/24 is TEST-NET-1, for documentation (RFC 5737).
		return fmt.Sprintf("192.0.2.%d", 1+f.rnd.IntN(254))
	case "ipv6":
		// 2001:db8::/32 is for documentation (RFC 3849).
		return fmt.Sprintf("2001:db8::%x", 1+f.rnd.IntN(0xfffe))
	case "uri", "iri", "url":
		return "https://example.com/" + f.word()
	case "uri-reference", "iri-reference", "json-pointer":
		return "/" + f.word()
	case "relative-json-pointer":
		return "0/" + f.word()
	case "uri-template":
		return "https://example.com/" + f.word() + "/{id}"
	case "uuid":
		return f.uuid()
	case "byte":
		return base64.StdEncoding.EncodeToString([]byte(f.words(2)))
	case "password":
		return f.token(12)
	case "regex":
		return "^[a-z]+$"
	default:
		return ""
	}
}

// instant returns a time within the three years before _epoch, to the second.
func (f *Faker) instant() time.Time {
	return _epoch.Add(-time.Duration(f.rnd.Int64N(3*365*24*3600)) * time.Second)
}

// uuid returns a version 4 UUID made of the Faker's random bits, so a seed makes the same ones.
func (f *Faker) uuid() string {
	var id uuid.UUID
	for i := range id {
		id[i] = byte(f.rnd.UintN(256))
	}

	id[6] = id[6]&0x0f | 0x40 // version 4
	id[8] = id[8]&0x3f | 0x80 // variant 10, RFC 9562

	return id.String()
}

// email returns an address at example.com, which is for documentation (RFC 2606).
func (f *Faker) email() string {
	return strings.ToLower(f.pick(_firstNames) + "." + f.pick(_lastNames) + "@example.com")
}

// token returns n letters and digits.
func (f *Faker) token(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	var b strings.Builder
	for range n {
		b.WriteByte(alphabet[f.rnd.IntN(len(alphabet))])
	}

	return b.String()
}
