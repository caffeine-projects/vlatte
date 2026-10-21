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
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
)

var (
	_ mockHTTPLifecycle                      = journalEvents{}
	_ vlatte.RequestPatterns[*Mock, *Server] = (*Server)(nil)
)

// errNoAnswer is the message of a request that ended without an outcome, such as one that panicked.
var errNoAnswer = errors.New("http: the request ended without an answer")

// openJournal opens the journal conf configures, nil when it configures none. A relative directory starts at the
// root directory, as the admin API's does.
func openJournal(conf *Config, logger *zerolog.Logger) (*vlatte.Journal, error) {
	if conf.Journal == nil {
		return nil, nil
	}

	dir := conf.Journal.Dir
	if dir != "" && !filepath.IsAbs(dir) {
		dir = filepath.Join(conf.RootDir, dir)
	}

	journal, err := vlatte.NewJournal(vlatte.JournalOptions{
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
	r.journal.record(r, vlatte.OutcomeMatched, statusOf(res), nil)
}

func (j journalEvents) OnProxied(r *RequestValues, res *Response) {
	r.journal.record(r, vlatte.OutcomeProxied, statusOf(res), nil)
}

func (j journalEvents) OnNoMatch(r *RequestValues) {
	r.journal.record(r, vlatte.OutcomeUnmatched, j.app.config.RequestWasNotMatchedStatusCode, nil)
}

// OnWarning keeps the warning, which tells why a request that reaches no outcome ended.
func (j journalEvents) OnWarning(r *RequestValues, err error) {
	r.journal.warn(err)
}

func (j journalEvents) OnError(r *RequestValues, err error) {
	r.journal.record(r, vlatte.OutcomeFailed, j.app.config.ErrorStatusCode, err)
}

func statusOf(res *Response) int {
	if res == nil {
		return 0
	}

	return res.StatusCode
}

// journalCall is the entry of a request in the server's journal, which it appends once the request reaches an
// outcome: the first one counts.
type journalCall struct {
	journal *vlatte.Journal
	mu      sync.Mutex
	entry   vlatte.JournalEntry
	warning error
	done    bool
}

func newJournalCall(app *Server, r *RequestValues) *journalCall {
	req := r.RawRequest
	body, truncated := journalBody(app.config, r)

	// The Host header is no header of net/http's: the entry has it, as the client sent it.
	header := redactHeader(req.Header, app.config.HeaderNamesToRedact)
	if len(app.config.HeaderNamesToRedact) == 0 {
		header = req.Header.Clone()
	}

	if req.Host != "" {
		header.Set("Host", req.Host)
	}

	return &journalCall{
		journal: app.Journal(),
		entry: vlatte.JournalEntry{
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

// journalBody returns what the journal keeps of the body of r: what the server read of it, up to the journal's limit,
// and whether that is less than the body. A server that reads no body, under NoBodyParsing, or of a GET or a HEAD,
// keeps none of one.
func journalBody(conf *Config, r *RequestValues) ([]byte, bool) {
	req, body := r.RawRequest, r.RawBodyBytes

	unread := conf.NoBodyParsing || req.Method == http.MethodGet || req.Method == http.MethodHead
	truncated := unread && req.ContentLength != 0 ||
		conf.MaxBodyParsingLimit > 0 && int64(len(body)) >= conf.MaxBodyParsingLimit ||
		req.ContentLength > int64(len(body))

	if limit := conf.Journal.MaxBodySize; limit > 0 && int64(len(body)) > limit {
		body, truncated = vlatte.ClipBody(body, int(limit)), true
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

// record appends the entry of r with its outcome, unless it has one already. A nil c journals nothing.
func (c *journalCall) record(r *RequestValues, outcome vlatte.Outcome, status int, err error) {
	if c == nil {
		return
	}

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
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.warning = err
}

// end appends the entry of r, a request that ended, if it reached no outcome: abandoned, when its client left, or
// failed, such as by a panic, with its last warning.
func (c *journalCall) end(r *RequestValues) {
	if c == nil {
		return
	}

	c.mu.Lock()
	warning := c.warning
	c.mu.Unlock()

	if ctx := r.RawRequest.Context(); ctx.Err() != nil {
		c.record(r, vlatte.OutcomeAbandoned, 0, cmp.Or(warning, context.Cause(ctx)))
		return
	}

	c.record(r, vlatte.OutcomeFailed, 0, cmp.Or(warning, errNoAnswer))
}

// RequestPattern returns the filter of journal entries that the request part of b matches, as it matches a request:
// b is a *Builder, such as Request(), whose reply counts for nothing. Times, Once and scenarios match no request
// served before, so a pattern has none. It implements vlatte.RequestPatterns.
func (app *Server) RequestPattern(b vlatte.Builder[*Mock, *Server]) (vlatte.EntryFilter, error) {
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
	}

	evals := vlatte.Evaluators[*Mock, *selectorInput](slices.Clone(builder.spec.expectations))

	return func(ctx context.Context, e *vlatte.JournalEntry) (bool, error) {
		return app.matchEntry(ctx, e, evals)
	}, nil
}

// matchEntry tells whether evals match the request of e, an entry of the server's journal, as it came. The request
// goes through the server's body parsers, as it did.
func (app *Server) matchEntry(ctx context.Context, e *vlatte.JournalEntry, evals vlatte.Evaluators[*Mock, *selectorInput]) (bool, error) {
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

	return evals.Eval(ctx, vlatte.NewExplanation(), newSelectorInput(req, rv, false))
}

// entryRequest rebuilds the request of e, in origin form: the server reads its URL as its own, as it read the
// request. The Host header goes in Host, as net/http has it.
func entryRequest(ctx context.Context, e *vlatte.JournalEntry) (*http.Request, error) {
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
		URL:           &url.URL{Path: u.Path, RawPath: u.RawPath, RawQuery: u.RawQuery, Fragment: u.Fragment, RawFragment: u.RawFragment},
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
