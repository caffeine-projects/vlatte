package handler

import (
	"io"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/value"
)

const (
	_formatJSON = "json"
	_formatYAML = "yaml"

	// _maxBody is the size of the largest body the admin API reads: a mock can carry its response body inline.
	_maxBody = 10 << 20

	// _accepted lists the media types the admin API reads and answers in.
	_accepted = "application/json, application/yaml"
)

// _requestFormats are the formats of the media types a request body can have.
var _requestFormats = map[string]string{
	"application/json":   _formatJSON,
	"application/yaml":   _formatYAML,
	"application/x-yaml": _formatYAML,
	"text/yaml":          _formatYAML,
	"text/x-yaml":        _formatYAML,
}

// _answerFormats lists the formats the admin API answers in, with the names Accept can give each, and the media type
// of their problem documents. JSON comes first: it wins ties.
var _answerFormats = []struct {
	format      string
	contentType string
	names       []string
	problemType string
}{
	{
		format: _formatJSON, contentType: "application/json",
		names:       []string{"application/json"},
		problemType: "application/problem+json",
	},
	{
		format: _formatYAML, contentType: "application/yaml",
		names:       []string{"application/yaml", "application/x-yaml", "text/yaml", "text/x-yaml"},
		problemType: "application/problem+yaml",
	},
}

// acceptRange is a media range of an Accept header, and its weight.
type acceptRange struct {
	mediaType string
	q         float64
}

// readBody reads the body of r, in the format its Content-Type names: JSON when it names none.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, string, error) {
	format := _formatJSON

	if ct := r.Header.Get("Content-Type"); ct != "" {
		mediaType, _, err := mime.ParseMediaType(ct)
		f, ok := _requestFormats[mediaType]
		if err != nil || !ok {
			w.Header().Set("Accept", _accepted)
			return nil, "", &problemError{
				status: http.StatusUnsupportedMediaType, code: _codeUnsupportedMediaType,
				detail: "the body's media type " + strconv.Quote(ct) + " is not JSON or YAML: send " + _accepted,
			}
		}

		format = f
	}

	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, _maxBody))
	if err != nil {
		return nil, "", err
	}

	return data, format, nil
}

// decodeBody applies a document in format to dst through schema. An empty document changes nothing.
func decodeBody[T any](schema *config.Schema[T], dst *T, data []byte, format string) error {
	if strings.TrimSpace(string(data)) == "" {
		return nil
	}

	_, err := schema.Load(dst, []config.Source{config.Bytes("admin request", format, data)})

	return err
}

// responseFormat returns the format r accepts answers in: the one Accept gives the most weight, JSON on a tie or
// without Accept. It reports false when Accept takes neither.
func responseFormat(r *http.Request) (string, bool) {
	return acceptedFormat(r, false)
}

// problemFormat returns the format r accepts problem documents in: as responseFormat, but a format's problem media
// type, such as application/problem+json, names it too.
func problemFormat(r *http.Request) (string, bool) {
	return acceptedFormat(r, true)
}

func acceptedFormat(r *http.Request, problem bool) (string, bool) {
	header := r.Header.Values("Accept")
	if len(header) == 0 {
		return _formatJSON, true
	}

	ranges := parseAccept(header)

	best, bestQ := "", 0.0
	for _, f := range _answerFormats {
		names := f.names
		if problem {
			names = append(slices.Clone(names), f.problemType)
		}

		if q := quality(ranges, names); q > bestQ {
			best, bestQ = f.format, q
		}
	}

	return best, best != ""
}

// parseAccept parses the media ranges of Accept headers, leaving out those it can't parse.
func parseAccept(header []string) []acceptRange {
	var ranges []acceptRange

	for _, h := range header {
		for item := range strings.SplitSeq(h, ",") {
			mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(item))
			if err != nil {
				continue
			}

			q := 1.0
			if v, ok := params["q"]; ok {
				if q, err = strconv.ParseFloat(v, 64); err != nil {
					continue
				}
			}

			ranges = append(ranges, acceptRange{mediaType: mediaType, q: q})
		}
	}

	return ranges
}

// quality returns the weight ranges give a format, whose media types have names: the weight of the most specific
// range that matches a name, the highest of those when several are as specific.
func quality(ranges []acceptRange, names []string) float64 {
	specificity, q := 0, 0.0

	for _, rng := range ranges {
		s := 0
		for _, name := range names {
			s = max(s, rng.matches(name))
		}

		if s > specificity || s > 0 && s == specificity && rng.q > q {
			specificity, q = s, rng.q
		}
	}

	return q
}

// matches tells how specifically the range matches mediaType: 3 by name, 2 by type, as text/*, 1 as */*, 0 not.
func (a acceptRange) matches(mediaType string) int {
	switch a.mediaType {
	case mediaType:
		return 3
	case "*/*":
		return 1
	}

	prefix, ok := strings.CutSuffix(a.mediaType, "*")
	if ok && strings.HasSuffix(prefix, "/") && strings.HasPrefix(mediaType, prefix) {
		return 2
	}

	return 0
}

// negotiated answers 406 to a request that accepts neither JSON nor YAML, before h does anything.
func negotiated(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := responseFormat(r); !ok {
			fail(w, r, &problemError{
				status: http.StatusNotAcceptable, code: _codeNotAcceptable,
				detail: "Accept names no media type the admin API answers in: accept " + _accepted,
			})

			return
		}

		h(w, r)
	})
}

// respond answers v, a value with json tags, in the format r accepts.
func respond(w http.ResponseWriter, r *http.Request, status int, v any) {
	format, _ := responseFormat(r)

	body, err := encode(v, format)
	if err != nil {
		fail(w, r, err)
		return
	}

	write(w, status, contentType(format), body)
}

// encode encodes v, a value with json tags, in format.
func encode(v any, format string) ([]byte, error) {
	doc, err := value.ViaJSON(v)
	if err != nil {
		return nil, err
	}

	return config.Encode(doc, format)
}

func contentType(format string) string {
	for _, f := range _answerFormats {
		if f.format == format {
			return f.contentType
		}
	}

	return _answerFormats[0].contentType
}

func problemType(format string) string {
	for _, f := range _answerFormats {
		if f.format == format {
			return f.problemType
		}
	}

	return _answerFormats[0].problemType
}

// write writes an answer whose body is whole: headers can't change once the status is written.
func write(w http.ResponseWriter, status int, contentType string, body []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Vary", "Accept")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
