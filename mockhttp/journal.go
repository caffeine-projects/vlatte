package mockhttp

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/journal"
)

var (
	_ mockHTTPLifecycle                       = journalEvents{}
	_ journal.RequestPatterns[*Mock, *Server] = (*Server)(nil)
)

var (
	// errNoAnswer is the message of a request that ended without an outcome.
	errNoAnswer = errors.New("http: the request ended without an answer")

	// errPanicked is the message of a request whose serving panicked.
	errPanicked = errors.New("http: the request panicked")
)

// openJournal opens the journal conf configures, nil when it configures none. Its directory reads as the admin API's
// does: a relative one starts at the root directory, and WorkDirVar at the work directory.
func openJournal(conf *Config, logger *zerolog.Logger) (*journal.Journal, error) {
	if conf.Journal == nil {
		return nil, nil
	}

	dir := conf.Journal.Dir
	if dir != "" {
		dir = conf.path(dir)
	}

	journal, err := journal.New(journal.Options{
		Dir:         dir,
		MaxEntries:  conf.Journal.MaxEntries,
		MaxBodySize: conf.Journal.MaxBodySize,
		Logger:      logger,
	})
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}

	return journal, nil
}

// withJournal adds the journal's events to lifecycle, when the server keeps a journal.
func withJournal(app *Server, conf *Config, lifecycle mockHTTPLifecycle) mockHTTPLifecycle {
	if conf.Journal == nil {
		return lifecycle
	}

	return lifecycles{lifecycle, journalEvents{app: app}}
}

// journalEvents journals the requests the server serves: each request's entry takes the first outcome its
// serving reaches, and ServeHTTP ends those that reach none.
type journalEvents struct {
	app *Server
}

// OnRequest takes the request as it came: mappers, callbacks and proxies may change it later.
func (j journalEvents) OnRequest(r *RequestValues) {
	r.journal = newJournalCall(j.app, r)
}

func (j journalEvents) OnMatch(r *RequestValues, res *Response) {
	r.journal.record(r, journal.OutcomeMatched, r.answered(), nil)
}

func (j journalEvents) OnProxied(r *RequestValues, res *Response) {
	r.journal.record(r, journal.OutcomeProxied, r.answered(), nil)
}

func (j journalEvents) OnNoMatch(r *RequestValues) {
	r.journal.record(r, journal.OutcomeUnmatched, r.answered(), nil)
}

// OnWarning keeps the warning, which tells why a request that reaches no outcome ended.
func (j journalEvents) OnWarning(r *RequestValues, err error) {
	r.journal.warn(err)
}

// OnError journals the status the client got: a reply may have sent one before it failed.
func (j journalEvents) OnError(r *RequestValues, err error) {
	r.journal.record(r, journal.OutcomeFailed, r.answered(), err)
}

// journalCall is the entry of a request in the server's journal, which it appends once the request reaches an
// outcome: the first one counts.
type journalCall struct {
	journal *journal.Journal
	mu      sync.Mutex
	entry   journal.Entry
	warning error
	done    bool
}

func newJournalCall(app *Server, r *RequestValues) *journalCall {
	req := r.RawRequest
	body, truncated := journalBody(app.config, r)

	// The Host header is no header of net/http's: the entry has it, as the client sent it, redacted as any other.
	header := req.Header.Clone()
	if req.Host != "" {
		header.Set("Host", req.Host)
	}

	header = redactHeader(header, app.config.HeaderNamesToRedact)

	return &journalCall{
		journal: app.Journal(),
		entry: journal.Entry{
			ID:            r.ReqID,
			Time:          r.StartedAtTime,
			Kind:          Kind,
			Method:        req.Method,
			Path:          r.URL.Path,
			Target:        requestTarget(req, r.URL),
			Header:        header,
			Body:          body,
			BodyTruncated: truncated,
		},
	}
}

// journalBody returns what the journal takes of the body of r, and whether the server read less than the body. A
// server that reads no body, under NoBodyParsing, or of a GET or a HEAD, has none of one. The journal clips the body
// to its limit, between characters: it takes a character's bytes past the limit.
func journalBody(conf *Config, r *RequestValues) ([]byte, bool) {
	req, body := r.RawRequest, r.RawBodyBytes

	unread := conf.NoBodyParsing || req.Method == http.MethodGet || req.Method == http.MethodHead
	truncated := unread && req.ContentLength != 0 || bodyCut(conf.MaxBodyParsingLimit, req, body)

	if limit := conf.Journal.MaxBodySize; limit > 0 {
		body = body[:min(int64(len(body)), limit+utf8.UTFMax)]
	}

	return bytes.Clone(body), truncated
}

// requestTarget returns what req asked for in full: the host of a CONNECT, the URL a request to the forward proxy
// names, or the server's URL u of another.
func requestTarget(req *http.Request, u *url.URL) string {
	switch {
	case req.Method == http.MethodConnect:
		return req.Host
	case req.URL.IsAbs():
		return req.URL.String()
	}

	return u.String()
}

// record appends the entry of r with its outcome, unless it has one already.
func (c *journalCall) record(r *RequestValues, outcome journal.Outcome, status int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.done {
		return
	}

	c.done = true
	c.entry.Outcome, c.entry.Status = outcome, status
	c.entry.Elapsed = time.Since(c.entry.Time)
	c.entry.NearMisses = r.nearMisses

	if r.Mock != nil {
		c.entry.MockID = r.Mock.ID()
	}

	if err != nil {
		c.entry.Message = err.Error()
	}

	// The journal logs what it fails to keep.
	c.journal.Append(&c.entry)
}

// warn keeps err, the last warning of the request, which tells why it ended, if it reaches no outcome.
func (c *journalCall) warn(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.warning = err
}

// end appends the entry of r, a request that ended, if it reached no outcome: failed, when it panicked with p, abandoned,
// when its client left, which left tells, or failed, with its last warning, with the status sent.
func (c *journalCall) end(r *RequestValues, p any, left error) {
	c.mu.Lock()
	warning := c.warning
	c.mu.Unlock()

	// net/http drops the connection of an answer aborted: the client has what was sent, as when the handler ends.
	abort, _ := p.(error)
	aborted := errors.Is(abort, http.ErrAbortHandler)

	switch {
	case p != nil && !aborted:
		c.record(r, journal.OutcomeFailed, r.panicked(), fmt.Errorf("%w: %v", errPanicked, p))
	case left != nil:
		c.record(r, journal.OutcomeAbandoned, r.sent(), cmp.Or(warning, left))
	case aborted:
		c.record(r, journal.OutcomeFailed, r.sent(), cmp.Or(warning, abort))
	default:
		c.record(r, journal.OutcomeFailed, r.sent(), cmp.Or(warning, errNoAnswer))
	}
}

// RequestPattern returns the filter of journal entries that the request part of b matches, as it matches a request:
// b is a *Builder, such as Request(), whose reply counts for nothing. Times, Once and scenarios match no request
// served before, so a pattern has none. It implements journal.RequestPatterns.
func (app *Server) RequestPattern(b vlatte.Builder[*Mock, *Server]) (journal.Filter, error) {
	builder, ok := b.(*Builder)

	switch {
	case !ok:
		return nil, fmt.Errorf("http: a request pattern is a *Builder, such as Request(), not %T", b)
	case builder.err != nil:
		return nil, fmt.Errorf("http: request pattern: %w", builder.err)
	case builder.times > 0:
		return nil, errors.New("http: a request pattern has no Times or Once: they count the requests a mock serves")
	case builder.scenario != "":
		return nil, errors.New("http: a request pattern has no scenario: its state is now's, not the request's")
	case slices.ContainsFunc(builder.spec.expectations, func(e *expectation) bool {
		return matchingScope(e.Target) == matchingScopeClientIP
	}):
		return nil, errors.New("http: a request pattern has no ClientIP: the journal keeps no client address")
	}

	params, err := builder.pathParamExpectations()
	if err != nil {
		return nil, fmt.Errorf("http: request pattern: %w", err)
	}

	evals := vlatte.Evaluators[*Mock, *selectorInput](slices.Concat(builder.spec.expectations, params))

	// A pattern that reads the body can't tell whether a body the journal cut matched: the rest of the pattern runs
	// first, as a miss there is a miss.
	var rest vlatte.Evaluators[*Mock, *selectorInput]

	for _, e := range evals {
		if !readsBody(matchingScope(e.Target)) {
			rest = append(rest, e)
		}
	}

	return func(ctx context.Context, e *journal.Entry) (bool, error) {
		if !e.BodyTruncated || len(rest) == len(evals) {
			return app.matchEntry(ctx, e, evals)
		}

		ok, err := app.matchEntry(ctx, e, rest)
		if err != nil || !ok {
			return false, err
		}

		return false, fmt.Errorf("http: %w: entry %d: the journal kept part of its body, which the pattern reads",
			vlatte.ErrMatching, e.Seq)
	}, nil
}

// readsBody reports whether a matcher of scope reads the body of a request: one of the body or of a form does, and so
// may a predicate of the whole request, RequestMatches.
func readsBody(scope matchingScope) bool {
	switch scope {
	case matchingScopeBody, matchingScopeFormField, matchingScopeFormFields, matchingScopeFormFile, matchingScopeNone:
		return true
	}

	return false
}

// adminRequestPattern returns the filter of the request part of a mock file, v, the request key of a query of the
// admin API: its keys are a mock file's, and it matches as a mock does. An empty one matches every request.
func (app *Server) adminRequestPattern(v config.Value) (journal.Filter, error) {
	def := newMockDef()
	doc := config.Value{Origin: v.Origin, Any: map[string]any{"request": v.Any}}

	problems := config.ProblemsOf(doc, _mockSchema.Decode(&def, doc))
	problems = append(problems, def.request.conflicts(v.Origin)...)

	if len(problems) > 0 {
		return nil, &config.Error{Problems: problems}
	}

	b := Request()
	def.request.apply(b)

	return app.RequestPattern(b)
}

// matchEntry tells whether evals match the request of e, an entry of the server's journal, as it came. The request
// goes through the server's body parsers, as it did.
func (app *Server) matchEntry(ctx context.Context, e *journal.Entry, evals vlatte.Evaluators[*Mock, *selectorInput]) (bool, error) {
	if e.Kind != Kind {
		return false, nil
	}

	req, err := entryRequest(ctx, e)
	if err != nil {
		return false, err
	}

	rv, _, _ := parseRequest(req, app)
	if req.MultipartForm != nil {
		defer req.MultipartForm.RemoveAll()
	}

	ok, err := evals.Eval(ctx, vlatte.NewExplanation(), newSelectorInput(req, rv, false))
	if err != nil {
		return false, fmt.Errorf("http: %w: entry %d: %w", vlatte.ErrMatching, e.Seq, err)
	}

	return ok, nil
}

// entryRequest rebuilds the request of e, with its target's URL: the server reads the scheme and host of a request to
// the forward proxy, or an intercepted one, as it read them, and its own of another, which its target has. The Host
// header goes in Host, as net/http has it.
func entryRequest(ctx context.Context, e *journal.Entry) (*http.Request, error) {
	u := &url.URL{Host: e.Target}

	if e.Method != http.MethodConnect {
		var err error
		if u, err = url.Parse(e.Target); err != nil {
			return nil, fmt.Errorf("http: entry %d: target: %w", e.Seq, err)
		}
	}

	header := http.Header(e.Header).Clone()
	if header == nil {
		header = http.Header{}
	}

	host := cmp.Or(header.Get("Host"), u.Host)
	header.Del("Host")

	req := &http.Request{
		Method:        e.Method,
		URL:           &url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path, RawPath: u.RawPath, RawQuery: u.RawQuery, Fragment: u.Fragment, RawFragment: u.RawFragment},
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(e.Body)),
		ContentLength: int64(len(e.Body)),
		Host:          host,
		RequestURI:    u.RequestURI(),
	}

	return req.WithContext(ctx), nil
}
