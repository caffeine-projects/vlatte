package mockhttp

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/httpx"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/bufpool"
)

var _ NoMatchRenderer = noMatchRenderer{}

// _noMatchProblemType is the type of the problem details the default renderer answers a request no mock matched with:
// the page that explains the answer.
const _noMatchProblemType = "https://caffeine-projects.github.io/vlatte/http/troubleshooting#near-misses"

// errNoMatchFormatWithRenderer says a server has both a NoMatchRenderer and a no-match format, which configures the
// renderer the server makes without one.
var errNoMatchFormatWithRenderer = errors.New("config: no_match_format configures the default no-match renderer, " +
	"and the server has a NoMatchRenderer: pass the format to DefaultNoMatchRenderer")

// NoMatch is what the server knows of a request no mock matched, which a [NoMatchRenderer] answers.
type NoMatch struct {
	// Request is the request no mock matched.
	Request *RequestValues

	// NearMisses are the mocks that came closest to matching the request, closest first, as the journal records them.
	// It is empty when the server reports none, as with [ConfigBuilder.NoNearMisses]. They are a copy: changing them
	// changes neither the journal nor the events.
	NearMisses []vlatte.NearMiss

	// Status is the status the server answers a request no mock matched with: [StatusNoMatch] unless
	// [ConfigBuilder.MockNotFoundStatusCode] sets another.
	Status int
}

// NoMatchRenderer writes the answer to a request no mock matched. The server calls it for many requests at once.
//
// The server sets the VLatte-Error header before it calls RenderNoMatch: it tells a client that the server answered,
// not a mock, so a renderer should keep it. It sets X-Content-Type-Options: nosniff too, so a browser takes the
// answer, which can echo the request, for the Content-Type the renderer sets. A renderer that writes nothing answers with [NoMatch.Status] and no body.
// The journal, the events and the logs record the request whatever the renderer sends.
type NoMatchRenderer interface {
	RenderNoMatch(w http.ResponseWriter, nm *NoMatch)
}

// NoMatchFormat is the format of the answer [DefaultNoMatchRenderer] gives a request no mock matched.
type NoMatchFormat int

// No-match formats.
const (
	// NoMatchAuto picks the format the request's Accept header prefers, and adds Accept to the Vary header:
	// NoMatchJSON for application/json or a type such as application/vnd.api+json, NoMatchProblem only when Accept
	// prefers application/problem+json to application/json, NoMatchMarkdown for text/markdown, and NoMatchText
	// otherwise, as for a request without Accept or one that accepts only */*. Of types Accept weighs the same, the
	// one a more specific range names wins, such as application/json over */*, then the one Accept lists first.
	NoMatchAuto NoMatchFormat = iota
	// NoMatchText is a Markdown document sent as text/plain, which reads as well as it renders.
	NoMatchText
	// NoMatchMarkdown is the document of NoMatchText sent as text/markdown.
	NoMatchMarkdown
	// NoMatchJSON is an RFC 9457 problem details object sent as application/json. Next to the standard members, it
	// has request_id, request, with its method and url, and near_misses.
	NoMatchJSON
	// NoMatchProblem is the object of NoMatchJSON sent as application/problem+json.
	NoMatchProblem
)

// DefaultNoMatchRenderer returns the renderer of the answers to requests no mock matched in format, the one a server
// uses unless [ConfigBuilder.NoMatchRenderer] sets another. A custom renderer can hand it the requests it doesn't
// answer itself. It panics when format is not one of the NoMatch* formats.
func DefaultNoMatchRenderer(format NoMatchFormat) NoMatchRenderer {
	if !format.valid() {
		panic(fmt.Errorf("mockhttp: no-match format %d is not one of the NoMatch* formats", format))
	}

	return noMatchRenderer{format: format}
}

// valid tells whether f is one of the NoMatch* formats.
func (f NoMatchFormat) valid() bool {
	return f >= NoMatchAuto && f <= NoMatchProblem
}

// noMatchRendererOf returns the renderer of the answers to requests no mock matched that conf asks for.
func noMatchRendererOf(conf *Config) (NoMatchRenderer, error) {
	switch {
	case !conf.NoMatchFormat.valid():
		return nil, fmt.Errorf("config: no-match format %d is not one of the NoMatch* formats", conf.NoMatchFormat)
	case conf.NoMatchRenderer == nil:
		return DefaultNoMatchRenderer(conf.NoMatchFormat), nil
	case conf.NoMatchFormat != NoMatchAuto:
		return nil, errNoMatchFormatWithRenderer
	}

	return conf.NoMatchRenderer, nil
}

// noMatchRenderer is the renderer DefaultNoMatchRenderer returns.
type noMatchRenderer struct {
	format NoMatchFormat
}

// RenderNoMatch implements NoMatchRenderer.
func (nr noMatchRenderer) RenderNoMatch(w http.ResponseWriter, nm *NoMatch) {
	format := nr.format
	if format == NoMatchAuto {
		format = negotiateNoMatch(nm.Request.RawRequest.Header.Values(HeaderAccept))
		w.Header().Add(HeaderVary, HeaderAccept)
	}

	buf := bufpool.Get()
	buf.Reset()
	defer bufpool.Put(buf)

	switch format {
	case NoMatchJSON:
		w.Header().Set(HeaderContentType, MIMEApplicationJSON)
		writeNoMatchJSON(buf, nm)
	case NoMatchProblem:
		w.Header().Set(HeaderContentType, MIMEApplicationProblemJSON)
		writeNoMatchJSON(buf, nm)
	case NoMatchMarkdown:
		w.Header().Set(HeaderContentType, MIMETextMarkdownCharsetUTF8)
		writeNoMatchMarkdown(buf, nm)
	default:
		w.Header().Set(HeaderContentType, MIMETextPlainCharsetUTF8)
		writeNoMatchMarkdown(buf, nm)
	}

	w.WriteHeader(nm.Status)

	buf.WriteTo(w)
}

// negotiateNoMatch returns the format the Accept headers of a request prefer. Text answers a request without Accept,
// and wins when another format ties it on the same range, so a request that accepts only */* gets it. Problem details
// win only over application/json Accept prefers them to.
func negotiateNoMatch(accept []string) NoMatchFormat {
	if len(accept) == 0 {
		return NoMatchText
	}

	ranges := httpx.ParseAccept(accept)
	format, pref := NoMatchText, httpx.Prefer(ranges, MIMETextPlain)

	if p := httpx.Prefer(ranges, MIMETextMarkdown); prefers(p, pref, true) {
		format, pref = NoMatchMarkdown, p
	}

	jsonPref := preferJSON(ranges)
	if prefers(jsonPref, pref, true) {
		format, pref = NoMatchJSON, jsonPref
	}

	if p := httpx.Prefer(ranges, MIMEApplicationProblemJSON); prefers(p, pref, true) && prefers(p, jsonPref, false) {
		format = NoMatchProblem
	}

	return format
}

// preferJSON returns how much ranges want application/json. A range of another JSON type, such as
// application/vnd.api+json, names it too, unless a range names application/json itself, as one that refuses it does.
func preferJSON(ranges []httpx.AcceptRange) httpx.Preference {
	pref := httpx.Prefer(ranges, MIMEApplicationJSON)
	if pref.Specificity == 3 {
		return pref
	}

	for i, rng := range ranges {
		if !strings.HasSuffix(rng.MediaType, "+json") || rng.MediaType == MIMEApplicationProblemJSON {
			continue
		}

		if pref.Specificity < 3 || rng.Q > pref.Q {
			pref = httpx.Preference{Q: rng.Q, Specificity: 3, Index: i}
		}
	}

	return pref
}

// prefers tells whether Accept prefers a media type with p to one with o: p weighs more, or as much by a more specific
// range, or, when byOrder is set, as much by a range as specific that Accept lists first. A media type Accept weighs
// nothing is never preferred.
func prefers(p, o httpx.Preference, byOrder bool) bool {
	switch {
	case p.Q <= 0:
		return false
	case p.Q != o.Q:
		return p.Q > o.Q
	case p.Specificity != o.Specificity:
		return p.Specificity > o.Specificity
	}

	return byOrder && p.Index < o.Index
}

// writeNoMatchMarkdown writes the Markdown document that answers nm to buf: the request, the mocks that came closest,
// and what to do next. Every value of the request or of a mock is a code span on one line, so none can change the
// structure of the document.
func writeNoMatchMarkdown(buf *bytes.Buffer, nm *NoMatch) {
	r := nm.Request

	buf.WriteString("# Request did not match\n\nNo mock matched ")
	writeCodeSpan(buf, r.RawRequest.Method+" "+r.URL.String())
	buf.WriteString(".\n")

	if r.ReqID != "" {
		buf.WriteString("\nRequest ID: ")
		writeCodeSpan(buf, r.ReqID)
		buf.WriteString("\n")
	}

	if len(nm.NearMisses) > 0 {
		buf.WriteString("\n## Closest mocks\n\n")

		for i, miss := range nm.NearMisses {
			buf.WriteString(strconv.Itoa(i + 1))
			buf.WriteString(". ")
			writeCodeSpan(buf, miss.MockID)

			if miss.Name != "" && miss.Name != miss.MockID {
				buf.WriteString(" (")
				writeCodeSpan(buf, miss.Name)
				buf.WriteString(")")
			}

			fmt.Fprintf(buf, ": %d of %d parts matched", miss.Passed, miss.Total)
			writeCodeSpans(buf, "; differs in ", miss.Failed)
			writeCodeSpans(buf, "; not checked: ", miss.Skipped)
			buf.WriteString("\n")
		}
	}

	adminURL := ""
	if r.journal != nil && r.App != nil {
		adminURL = r.App.AdminURL()
	}

	if len(nm.NearMisses) == 0 && adminURL == "" {
		return
	}

	buf.WriteString("\n## Next steps\n\n")

	if len(nm.NearMisses) > 0 {
		buf.WriteString("- Change the request, or the closest mock, in the parts they differ in.\n")
	}

	if adminURL != "" {
		buf.WriteString("- See the request in the journal: ")
		writeCodeSpan(buf, "GET "+adminURL+"/requests/"+r.ReqID)
		buf.WriteString("\n")
	}
}

// writeCodeSpans writes values to buf after prefix, as code spans separated by commas, unless there are none.
func writeCodeSpans(buf *bytes.Buffer, prefix string, values []string) {
	if len(values) == 0 {
		return
	}

	buf.WriteString(prefix)

	for i, v := range values {
		if i > 0 {
			buf.WriteString(", ")
		}

		writeCodeSpan(buf, v)
	}
}

// writeCodeSpan writes s, which is not empty, to buf as a Markdown code span on one line: its delimiters are a run of backticks longer than
// any in s, and a space pads s when it starts or ends with a backtick, or with a space a reader would strip.
func writeCodeSpan(buf *bytes.Buffer, s string) {
	s = _oneLine.Replace(s)

	longest, run := 0, 0
	for _, c := range []byte(s) {
		if c != '`' {
			run = 0
			continue
		}

		run++
		longest = max(longest, run)
	}

	fence := strings.Repeat("`", longest+1)
	pad := strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") ||
		strings.HasPrefix(s, " ") && strings.HasSuffix(s, " ") && strings.TrimSpace(s) != ""

	buf.WriteString(fence)

	if pad {
		buf.WriteString(" ")
	}

	buf.WriteString(s)

	if pad {
		buf.WriteString(" ")
	}

	buf.WriteString(fence)
}

// _oneLine turns line breaks into spaces.
var _oneLine = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")

// writeNoMatchJSON writes the problem details object that answers nm to buf.
func writeNoMatchJSON(buf *bytes.Buffer, nm *NoMatch) {
	r := nm.Request

	p := NewProblem().
		Type(_noMatchProblemType).
		Title("Request did not match").
		Status(nm.Status).
		Detail("No mock matched "+r.RawRequest.Method+" "+r.URL.String()+".").
		Extension("request", noMatchRequest{Method: r.RawRequest.Method, URL: r.URL.String()})

	if r.ReqID != "" {
		p.Extension("request_id", r.ReqID)
	}

	if len(nm.NearMisses) > 0 {
		p.Extension("near_misses", nm.NearMisses)
	}

	// Neither fails: no extension is named after a standard member, every value encodes, and a buffer takes all. A
	// name or a URL that isn't UTF-8 gets the replacement character where it isn't.
	members, _ := p.members()
	_ = json.MarshalWrite(buf, members, json.Deterministic(true), jsontext.AllowInvalidUTF8(true))
}

// cloneNearMisses returns a copy of misses that shares nothing with it.
func cloneNearMisses(misses []vlatte.NearMiss) []vlatte.NearMiss {
	if misses == nil {
		return nil
	}

	clone := slices.Clone(misses)
	for i := range clone {
		clone[i].Failed = slices.Clone(clone[i].Failed)
		clone[i].Skipped = slices.Clone(clone[i].Skipped)
	}

	return clone
}

// noMatchRequest is the request member of the problem details of a request no mock matched.
type noMatchRequest struct {
	Method string `json:"method"`
	URL    string `json:"url"`
}
