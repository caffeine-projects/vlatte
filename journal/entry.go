package journal

import (
	"encoding"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/caffeine-projects/vlatte"
)

var (
	_ json.MarshalerTo         = (*Entry)(nil)
	_ json.UnmarshalerFrom     = (*Entry)(nil)
	_ encoding.TextMarshaler   = OutcomeUnknown
	_ encoding.TextUnmarshaler = (*Outcome)(nil)
)

// Outcome is what came of a request a mock server served.
type Outcome int8

const (
	// OutcomeUnknown is an outcome that could not be parsed.
	OutcomeUnknown Outcome = iota
	// OutcomeMatched is a request a mock answered.
	OutcomeMatched
	// OutcomeUnmatched is a request no mock matched, which the server answered as such.
	OutcomeUnmatched
	// OutcomeProxied is a request another server answered: the host of a forward proxy, or a proxy's target.
	OutcomeProxied
	// OutcomeFailed is a request whose serving failed.
	OutcomeFailed
	// OutcomeAbandoned is a request whose client left before it got its answer.
	OutcomeAbandoned
)

// String returns the outcome's name, or "unknown" for an unrecognized value.
func (o Outcome) String() string {
	switch o {
	case OutcomeMatched:
		return "matched"
	case OutcomeUnmatched:
		return "unmatched"
	case OutcomeProxied:
		return "proxied"
	case OutcomeFailed:
		return "failed"
	case OutcomeAbandoned:
		return "abandoned"
	default:
		return "unknown"
	}
}

// ParseOutcome parses an outcome's name in any case, returning [OutcomeUnknown] for an unrecognized name.
func ParseOutcome(s string) Outcome {
	switch strings.ToLower(s) {
	case "matched":
		return OutcomeMatched
	case "unmatched":
		return OutcomeUnmatched
	case "proxied":
		return OutcomeProxied
	case "failed":
		return OutcomeFailed
	case "abandoned":
		return OutcomeAbandoned
	default:
		return OutcomeUnknown
	}
}

// MarshalText encodes o as its name, keeping outcomes readable in journal files and API responses.
func (o Outcome) MarshalText() ([]byte, error) {
	return []byte(o.String()), nil
}

// UnmarshalText parses an outcome's name as [ParseOutcome] does.
func (o *Outcome) UnmarshalText(text []byte) error {
	*o = ParseOutcome(string(text))
	return nil
}

// Entry is a request a mock server served, as its [Journal] keeps it. The fields every protocol has are its
// own; a protocol fills those it has: gRPC has no method, and its path is the full method of the call.
type Entry struct {
	// Seq orders the entries of a journal: a later entry has a greater one. Append sets it.
	Seq uint64

	// ID is the request's, such as the one the VLatte-Request-Id header names.
	ID string

	// Time is when the request came.
	Time time.Time

	// Kind is the protocol of the request, such as "http".
	Kind vlatte.Kind

	Outcome Outcome

	// MockID is the mock that matched the request, if one did.
	MockID string

	Method string
	Path   string

	// Target is what the request asked for in full, such as its URL.
	Target string

	// Status is the status of the answer, such as an HTTP status or a gRPC code.
	Status int

	// Elapsed is how long the request took to serve.
	Elapsed time.Duration

	// Header holds the request's headers, or its metadata.
	Header map[string][]string

	// Body holds the request's body, up to the journal's limit: BodyTruncated tells it lost the rest.
	Body          []byte
	BodyTruncated bool

	// Message tells what failed: the request, for one that failed, or else its on_complete events that failed, such as
	// webhooks, whose errors it joins.
	Message string

	// NearMisses are the mocks that came closest to matching a request no mock matched.
	NearMisses []vlatte.NearMiss
}

// journalLine is the JSON form of an Entry, a line of its journal's files: a body that is text is kept as
// such, so a person reads it; another goes in base64. JSON has no durations: the elapsed time is in nanoseconds.
type journalLine struct {
	Seq           uint64              `json:"seq,omitzero"`
	ID            string              `json:"id"`
	Time          time.Time           `json:"time"`
	Kind          vlatte.Kind         `json:"kind"`
	Outcome       Outcome             `json:"outcome"`
	MockID        string              `json:"mock_id,omitempty"`
	Method        string              `json:"method,omitempty"`
	Path          string              `json:"path,omitempty"`
	Target        string              `json:"target,omitempty"`
	Status        int                 `json:"status,omitzero"`
	ElapsedNS     int64               `json:"elapsed_ns,omitzero"`
	Header        map[string][]string `json:"header,omitempty"`
	Body          string              `json:"body,omitempty"`
	BodyBase64    []byte              `json:"body_base64,omitempty"`
	BodyTruncated bool                `json:"body_truncated,omitzero"`
	Message       string              `json:"message,omitempty"`
	NearMisses    []vlatte.NearMiss   `json:"near_misses,omitempty"`
}

// MarshalJSONTo writes e as its journal's files keep it.
func (e *Entry) MarshalJSONTo(enc *jsontext.Encoder) error {
	line := e.line()
	return json.MarshalEncode(enc, &line)
}

// UnmarshalJSONFrom reads e as MarshalJSONTo writes it.
func (e *Entry) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var line journalLine
	if err := json.UnmarshalDecode(dec, &line); err != nil {
		return err
	}

	*e = line.entry()

	return nil
}

// validUTF8 replaces the bytes of the text of e that the index keeps, which aren't UTF-8, with U+FFFD, as the JSON of
// e has them: the index and the journal's files have the same values. Encoding replaces those of the rest.
func (e *Entry) validUTF8() {
	for _, s := range []*string{&e.ID, &e.MockID, &e.Method, &e.Path, &e.Target} {
		*s = strings.ToValidUTF8(*s, "\uFFFD")
	}

	e.Kind = vlatte.Kind(strings.ToValidUTF8(string(e.Kind), "\uFFFD"))
}

func (e *Entry) line() journalLine {
	line := journalLine{
		Seq:           e.Seq,
		ID:            e.ID,
		Time:          e.Time,
		Kind:          e.Kind,
		Outcome:       e.Outcome,
		MockID:        e.MockID,
		Method:        e.Method,
		Path:          e.Path,
		Target:        e.Target,
		Status:        e.Status,
		ElapsedNS:     e.Elapsed.Nanoseconds(),
		Header:        e.Header,
		BodyTruncated: e.BodyTruncated,
		Message:       e.Message,
		NearMisses:    e.NearMisses,
	}

	if utf8.Valid(e.Body) {
		line.Body = string(e.Body)
	} else {
		line.BodyBase64 = e.Body
	}

	return line
}

func (l *journalLine) entry() Entry {
	body := l.BodyBase64
	if l.Body != "" {
		body = []byte(l.Body)
	}

	return Entry{
		Seq:           l.Seq,
		ID:            l.ID,
		Time:          l.Time,
		Kind:          l.Kind,
		Outcome:       l.Outcome,
		MockID:        l.MockID,
		Method:        l.Method,
		Path:          l.Path,
		Target:        l.Target,
		Status:        l.Status,
		Elapsed:       time.Duration(l.ElapsedNS),
		Header:        l.Header,
		Body:          body,
		BodyTruncated: l.BodyTruncated,
		Message:       l.Message,
		NearMisses:    l.NearMisses,
	}
}
