package fakefy

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

var (
	_firstNames = []string{
		"Ada", "Alan", "Bella", "Buddy", "Carla", "Daniel", "Elena", "Felix", "Grace", "Hugo",
		"Iris", "Jonas", "Kira", "Leo", "Maya", "Nora", "Oscar", "Paula", "Rafael", "Sofia",
	}
	_lastNames = []string{
		"Almeida", "Baker", "Costa", "Davis", "Evans", "Fischer", "Garcia", "Hughes", "Ito", "Jensen",
		"Kowalski", "Lopez", "Martin", "Nakamura", "Oliveira", "Patel", "Quinn", "Rossi", "Silva", "Turner",
	}
	_cities = []string{
		"Amsterdam", "Berlin", "Buenos Aires", "Cape Town", "Dublin", "Lisbon", "Madrid", "Montreal", "Nairobi",
		"Osaka", "Porto", "Seoul", "Stockholm", "Sydney", "Toronto",
	}
	_countries     = []string{"Brazil", "Canada", "France", "Germany", "Ireland", "Japan", "Kenya", "Portugal", "Spain", "Sweden"}
	_countryCodes  = []string{"BR", "CA", "DE", "ES", "FR", "IE", "JP", "KE", "PT", "SE"}
	_currencies    = []string{"BRL", "CAD", "EUR", "GBP", "JPY", "USD"}
	_colors        = []string{"amber", "blue", "coral", "green", "indigo", "olive", "purple", "red", "teal", "white"}
	_companies     = []string{"Acme", "Bluebird", "Copperleaf", "Driftwood", "Evergreen", "Foxglove", "Granite", "Harbor"}
	_companyEnds   = []string{"Inc.", "Labs", "Ltd.", "Group", "Systems", "Works"}
	_streetEnds    = []string{"Avenue", "Lane", "Road", "Street", "Way"}
	_words         = []string{"amber", "bridge", "cedar", "delta", "ember", "field", "garden", "harbor", "island", "juniper", "kite", "lagoon", "meadow", "north", "orchard", "pebble", "quartz", "river", "summit", "timber", "valley", "willow"}
	_dateTimeNames = []string{"createdat", "updatedat", "deletedat", "modifiedat", "publishedat", "expiresat", "startsat", "endsat", "timestamp"}
)

// byName returns a string that looks like the field name holds, such as an email address for email or a first name
// for firstName, or "" for a name it knows nothing of.
func (f *Faker) byName(name string) string {
	n := normalize(name)

	switch {
	case n == "":
		return ""
	case strings.Contains(n, "email"):
		return f.email()
	case in(n, "firstname", "givenname", "name", "nickname"):
		return f.pick(_firstNames)
	case in(n, "lastname", "surname", "familyname"):
		return f.pick(_lastNames)
	case in(n, "fullname", "displayname", "author", "owner", "contact"):
		return f.pick(_firstNames) + " " + f.pick(_lastNames)
	case in(n, "username", "login", "handle"):
		return strings.ToLower(f.pick(_firstNames)) + strconv.Itoa(10+f.rnd.IntN(90))
	case in(n, "password", "passwd"):
		return f.token(12)
	case strings.Contains(n, "phone") || in(n, "mobile", "fax"):
		return fmt.Sprintf("+1-555-01%02d", f.rnd.IntN(100))
	case in(n, "avatar", "image", "photo", "picture", "thumbnail", "icon"):
		return "https://example.com/images/" + f.word() + ".jpg"
	case strings.HasSuffix(n, "url") || in(n, "href", "link", "website", "homepage", "uri"):
		return "https://example.com/" + f.word()
	case in(n, "city", "town"):
		return f.pick(_cities)
	case in(n, "country", "countryname"):
		return f.pick(_countries)
	case in(n, "countrycode"):
		return f.pick(_countryCodes)
	case in(n, "street", "address", "addressline", "addressline1", "streetaddress"):
		return fmt.Sprintf("%d %s %s", 1+f.rnd.IntN(999), capitalize(f.word()), f.pick(_streetEnds))
	case in(n, "zip", "zipcode", "postcode", "postalcode"):
		return fmt.Sprintf("%05d", f.rnd.IntN(100000))
	case in(n, "currency", "currencycode"):
		return f.pick(_currencies)
	case in(n, "price", "amount", "cost", "total", "balance", "fee", "salary"):
		return fmt.Sprintf("%d.%02d", 1+f.rnd.IntN(500), f.rnd.IntN(100))
	case in(n, "description", "summary", "bio", "about", "comment", "message", "note", "notes", "text", "content", "body"):
		return capitalize(f.words(5+f.rnd.IntN(4))) + "."
	case in(n, "title", "subject", "headline", "label"):
		return capitalize(f.words(2 + f.rnd.IntN(2)))
	case in(n, "company", "organization", "organisation", "employer"):
		return f.pick(_companies) + " " + f.pick(_companyEnds)
	case in(n, "color", "colour"):
		return f.pick(_colors)
	case in(n, "locale", "language", "lang"):
		return "en-US"
	case in(n, "token", "secret", "apikey", "hash", "checksum"):
		return f.token(32)
	case in(n, "ip", "ipaddress"):
		return f.format("ipv4")
	case in(n, "host", "hostname", "domain"):
		return f.format("hostname")
	case in(n, "slug"):
		return f.word() + "-" + f.word()
	case in(n, _dateTimeNames...) || strings.Contains(n, "date"):
		return f.format("date-time")
	case isID(name):
		return f.uuid()
	default:
		return ""
	}
}

// numberRange returns the range of numbers that look like what the field name holds, such as 1 to 90 for age, or
// false for a name it knows nothing of.
func numberRange(name string) (lo, hi float64, ok bool) {
	n := normalize(name)

	switch {
	case n == "":
		return 0, 0, false
	case in(n, "age"):
		return 1, 90, true
	case in(n, "year"):
		return 2000, 2026, true
	case in(n, "month"):
		return 1, 12, true
	case in(n, "day"):
		return 1, 28, true
	case in(n, "hour"):
		return 0, 23, true
	case in(n, "minute", "second"):
		return 0, 59, true
	case in(n, "price", "amount", "cost", "total", "balance", "fee", "salary"):
		return 1, 500, true
	case in(n, "quantity", "qty", "count", "page"):
		return 1, 10, true
	case in(n, "limit", "pagesize", "perpage"):
		return 10, 50, true
	case in(n, "latitude", "lat"):
		return -90, 90, true
	case in(n, "longitude", "lng", "lon", "long"):
		return -180, 180, true
	case in(n, "rating", "score", "stars"):
		return 1, 5, true
	case in(n, "percent", "percentage"):
		return 0, 100, true
	case isID(name):
		return 1, 1000, true
	default:
		return 0, 0, false
	}
}

// pick returns one of words.
func (f *Faker) pick(words []string) string {
	return words[f.rnd.IntN(len(words))]
}

// word returns a lowercase word.
func (f *Faker) word() string {
	return f.pick(_words)
}

// words returns n lowercase words, separated by spaces.
func (f *Faker) words(n int) string {
	w := make([]string, n)
	for i := range w {
		w[i] = f.word()
	}

	return strings.Join(w, " ")
}

// normalize returns name in lower case without separators, so first_name, first-name and firstName read the same.
func normalize(name string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '_', '-', ' ', '.':
			return -1
		default:
			return r
		}
	}, strings.ToLower(name))
}

// in reports whether n is one of names.
func in(n string, names ...string) bool {
	return slices.Contains(names, n)
}

// isID reports whether name names an identifier: id, or a word that ends in Id, ID, _id or -id, such as petId.
func isID(name string) bool {
	if strings.EqualFold(name, "id") {
		return true
	}

	for _, suffix := range []string{"Id", "ID", "_id", "-id"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}

	return false
}

// capitalize returns s, which isn't empty, with its first letter in upper case.
func capitalize(s string) string {
	return strings.ToUpper(s[:1]) + s[1:]
}
