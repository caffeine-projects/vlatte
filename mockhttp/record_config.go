package mockhttp

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/match"
)

var _ RecordConfigurer = (*RecordConfig)(nil)

const (
	_defaultRecordDir     = "testdata/_mocks_recorded"
	_defaultRecordFormat  = "json"
	_defaultMaxInlineBody = 1 << 20
	_defaultMaxBodySize   = 1 << 30
)

// RecordConfig configures recording: where the recordings go, what they match and what they keep. See
// Server.StartRecording.
type RecordConfig struct {
	// Dir is the directory of the recordings. Relative paths start at Config.RootDir. Defaults to
	// testdata/_mocks_recorded.
	Dir string

	// Format is the format of the recorded mock files: json, yaml or yml. Defaults to json.
	Format string

	// Target is the server to record: the requests the recording takes go to it, before any mock, and its answers are
	// recorded. It is an absolute http or https URL without a path, query or fragment, such as
	// https://api.example.com. Without one, the recording takes what the forward proxy and proxy replies answer.
	Target string

	// Methods are the methods of the requests the recording takes, in any case. None takes every method.
	Methods []string

	// Paths are glob patterns of the paths of the requests the recording takes, such as /v1/*, where * matches a "/"
	// too. None takes every path.
	Paths []string

	// Filter is a request pattern the requests the recording takes must match, made with Request(), such as
	// Request().Header("X-Record", match.Equal("yes")). The recording uses its request matchers only.
	Filter *Builder

	// SkipSSLVerify skips the verification of the Target's TLS certificate, such as for a self-signed one. A recording
	// sends the target its requests, credentials included, so the transport it makes verifies the target by default.
	SkipSSLVerify bool

	// Transport sends the requests to the Target, in place of the server's Config.Transport, such as one that trusts
	// the target's private CA. It is the caller's: the recording never closes it, and SkipSSLVerify doesn't apply to it.
	Transport http.RoundTripper

	// RequestHeaders are the request headers a recording matches: the first value each had, or its absence. One that
	// ConfigBuilder.RedactHeader names matches by its presence, so its value isn't written. A recording matches no
	// other header.
	RequestHeaders []string

	// ExcludeResponseHeaders are the response headers a recording leaves out, in any case. Defaults to Set-Cookie, so
	// mock files hold no session of the service. A list replaces the default.
	ExcludeResponseHeaders []string

	// BodyFiles writes every response body to a file of its own, next to the mock file, instead of inline.
	BodyFiles bool

	// MaxInlineBody is how many bytes of a body stay in memory while it is recorded: a larger one goes to a file as
	// it streams, at the pace of the disk, which it waits for a second in all at most: a disk slower than that drops
	// the recording, never the answer. A text body that stays in memory is written inline in the mock file. 0 means
	// 1 MiB.
	MaxInlineBody int64

	// MaxBodySize bounds the response bodies recorded: a call with a larger one isn't recorded. 0 means 1 GiB, and a
	// negative value means no limit.
	MaxBodySize int64

	// Repeats is what a recording keeps of a request that comes again: every response, in order, or the first one.
	// Defaults to vlatte.RecordSequence.
	Repeats vlatte.RecordRepeats

	// IncludeMocked records the answers of mocks too. Without it, only proxied answers are recorded: those of the
	// forward proxy and of proxy replies, such as From.
	IncludeMocked bool

	// Transforms change each recording before it is written, in order. A transform that fails skips the recording,
	// and so do transforms that leave it no response.
	Transforms []func(*RecordedMock) error
}

// RecordConfigurer lets users configure recording.
type RecordConfigurer interface {
	Apply(c *RecordConfig) error
}

type recordConfigFunc func(c *RecordConfig)

func (f recordConfigFunc) Apply(c *RecordConfig) error {
	f(c)
	return nil
}

func defaultRecordConfig() *RecordConfig {
	return &RecordConfig{
		Dir:                    _defaultRecordDir,
		Format:                 _defaultRecordFormat,
		ExcludeResponseHeaders: []string{"Set-Cookie"},
		MaxInlineBody:          _defaultMaxInlineBody,
		MaxBodySize:            _defaultMaxBodySize,
		Repeats:                vlatte.RecordSequence,
	}
}

// Apply sets the fields of r that aren't zero on c, and adds r's Transforms to c's, so configs compose. A config
// turns no setting off: options such as RecordBodyFiles(false) do.
func (r *RecordConfig) Apply(c *RecordConfig) error {
	c.Dir = cmp.Or(r.Dir, c.Dir)
	c.Format = cmp.Or(r.Format, c.Format)
	c.Target = cmp.Or(r.Target, c.Target)
	c.Filter = cmp.Or(r.Filter, c.Filter)
	c.SkipSSLVerify = c.SkipSSLVerify || r.SkipSSLVerify
	c.Transport = cmp.Or(r.Transport, c.Transport)
	c.BodyFiles = c.BodyFiles || r.BodyFiles
	c.MaxInlineBody = cmp.Or(r.MaxInlineBody, c.MaxInlineBody)
	c.MaxBodySize = cmp.Or(r.MaxBodySize, c.MaxBodySize)
	c.Repeats = cmp.Or(r.Repeats, c.Repeats)
	c.IncludeMocked = c.IncludeMocked || r.IncludeMocked
	c.Transforms = slices.Concat(c.Transforms, r.Transforms)

	if r.RequestHeaders != nil {
		c.RequestHeaders = slices.Clone(r.RequestHeaders)
	}

	if r.ExcludeResponseHeaders != nil {
		c.ExcludeResponseHeaders = slices.Clone(r.ExcludeResponseHeaders)
	}

	if r.Methods != nil {
		c.Methods = slices.Clone(r.Methods)
	}

	if r.Paths != nil {
		c.Paths = slices.Clone(r.Paths)
	}

	return nil
}

// clone returns a copy of r with lists of its own.
func (r *RecordConfig) clone() *RecordConfig {
	c := *r
	c.RequestHeaders = slices.Clone(r.RequestHeaders)
	c.ExcludeResponseHeaders = slices.Clone(r.ExcludeResponseHeaders)
	c.Methods = slices.Clone(r.Methods)
	c.Paths = slices.Clone(r.Paths)
	c.Transforms = slices.Clone(r.Transforms)

	return &c
}

// check checks the settings, and writes them as the recording reads them: the format as mock files name it, such as
// yaml for .YAML, the default repeats for none, and the methods and paths without the spaces around them.
func (r *RecordConfig) check() error {
	format, ok := config.Format(r.Format)
	if !ok {
		return fmt.Errorf("recorder: format %q is not one of %s", r.Format, strings.Join(MockFileFormats(), ", "))
	}

	r.Format = format
	r.Repeats = cmp.Or(r.Repeats, vlatte.RecordSequence)

	switch {
	case r.Dir == "":
		return errors.New("recorder: no directory to record into")
	case r.Repeats != vlatte.RecordSequence && r.Repeats != vlatte.RecordFirst:
		return fmt.Errorf("recorder: repeats %d is not vlatte.RecordSequence or vlatte.RecordFirst", r.Repeats)
	case r.MaxInlineBody < 0:
		return fmt.Errorf("recorder: MaxInlineBody must be 0 or more, not %d", r.MaxInlineBody)
	case r.Filter != nil && r.Filter.err != nil:
		return fmt.Errorf("recorder: filter: %w", r.Filter.err)
	}

	var err error
	if r.Methods, err = trimmed("method", r.Methods); err != nil {
		return err
	}

	if r.Paths, err = trimmed("path", r.Paths); err != nil {
		return err
	}

	if r.Target != "" {
		if err := checkRecordTarget(r.Target); err != nil {
			return fmt.Errorf("recorder: target %q: %w", config.Display(r.Target), err)
		}
	}

	return nil
}

// trimmed returns items without the spaces around them, in a slice of its own: the caller's may be shared.
func trimmed(what string, items []string) ([]string, error) {
	if items == nil {
		return nil, nil
	}

	out := make([]string, len(items))
	for i, item := range items {
		if out[i] = strings.TrimSpace(item); out[i] == "" {
			return nil, fmt.Errorf("recorder: %s %d is empty", what, i)
		}
	}

	return out, nil
}

// filter returns the matchers of the requests the recording takes: its methods, its paths and its filter's.
func (r *RecordConfig) filter() vlatte.Evaluators[*Mock, *selectorInput] {
	b := Request()

	if len(r.Methods) > 0 {
		methods := make([]vlatte.Matcher, len(r.Methods))
		for i, m := range r.Methods {
			methods[i] = match.EqualIgnoreCase(m)
		}

		b.MethodMatches(match.Any(methods...))
	}

	if len(r.Paths) > 0 {
		globs := make([]vlatte.Matcher, len(r.Paths))
		for i, p := range r.Paths {
			globs[i] = match.GlobMatch(p)
		}

		b.URLPath(match.Any(globs...))
	}

	evals := b.spec.expectations
	if r.Filter != nil {
		evals = append(evals, r.Filter.spec.expectations...)
	}

	return evals
}

// checkRecordTarget checks the URL of a server to record: an absolute http or https URL, without the parts the
// requests it gets have of their own: a path, a query, a fragment, and credentials.
func checkRecordTarget(target string) error {
	u, err := url.Parse(target)

	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		return errors.New("expected an absolute http or https URL, such as https://api.example.com")
	case (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		return errors.New("expected no path, query or fragment: the requests recorded keep theirs")
	case u.User != nil:
		return errors.New("expected no user info: the requests recorded keep their credentials")
	default:
		return nil
	}
}

// dir returns the directory of the recordings: Dir, from root when it is relative.
func (r *RecordConfig) dir(root string) string {
	if filepath.IsAbs(r.Dir) {
		return r.Dir
	}

	return filepath.Join(root, r.Dir)
}

// recordedMockFiles returns the glob patterns of the mock files a recording writes into dir.
func recordedMockFiles(dir string) []string {
	formats := MockFileFormats()

	patterns := make([]string, len(formats))
	for i, format := range formats {
		patterns[i] = dir + "/*.mock." + format
	}

	return patterns
}

//
// Recording Configuration Functions.
//

// RecordDir sets the directory of the recordings. Relative paths start at Config.RootDir.
func RecordDir(dir string) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.Dir = dir })
}

// RecordFormat sets the format of the recorded mock files: json, yaml or yml.
func RecordFormat(format string) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.Format = format })
}

// RecordRequestHeaders sets the request headers recordings match: the first value each had, or its absence.
func RecordRequestHeaders(names ...string) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.RequestHeaders = names })
}

// RecordExcludeResponseHeaders sets the response headers recordings leave out, in any case, in place of the default
// Set-Cookie. With no names, recordings keep every response header.
func RecordExcludeResponseHeaders(names ...string) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.ExcludeResponseHeaders = names })
}

// RecordTarget sets the server to record. See RecordConfig.Target.
func RecordTarget(target string) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.Target = target })
}

// RecordMethods sets the methods of the requests the recording takes, in any case.
func RecordMethods(methods ...string) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.Methods = methods })
}

// RecordPaths sets glob patterns of the paths of the requests the recording takes, such as /v1/*, where * matches a
// "/" too.
func RecordPaths(globs ...string) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.Paths = globs })
}

// RecordFilter sets a request pattern the requests the recording takes must match, made with Request(). See
// RecordConfig.Filter.
func RecordFilter(filter *Builder) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.Filter = filter })
}

// RecordSkipSSLVerify sets whether the verification of the target's TLS certificate is skipped. See
// RecordConfig.SkipSSLVerify.
func RecordSkipSSLVerify(v bool) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.SkipSSLVerify = v })
}

// RecordTransport sets the transport that sends the requests to the target. See RecordConfig.Transport.
func RecordTransport(rt http.RoundTripper) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.Transport = rt })
}

// RecordBodyFiles sets whether every response body goes to a file of its own, instead of inline.
func RecordBodyFiles(v bool) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.BodyFiles = v })
}

// RecordMaxInlineBody sets how many bytes of a body stay in memory while it is recorded. See
// RecordConfig.MaxInlineBody.
func RecordMaxInlineBody(n int64) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.MaxInlineBody = n })
}

// RecordMaxBodySize bounds the response bodies recorded. See RecordConfig.MaxBodySize.
func RecordMaxBodySize(n int64) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.MaxBodySize = n })
}

// RecordRepeats sets what a recording keeps of a request that comes again: vlatte.RecordSequence, the default, or
// vlatte.RecordFirst.
func RecordRepeats(repeats vlatte.RecordRepeats) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.Repeats = repeats })
}

// RecordIncludeMocked sets whether the answers of mocks are recorded too, not only proxied ones.
func RecordIncludeMocked(v bool) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.IncludeMocked = v })
}

// RecordTransform adds a transform, which changes each recording before it is written. A transform that fails skips
// the recording.
func RecordTransform(transform func(*RecordedMock) error) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) {
		c.Transforms = slices.Concat(c.Transforms, []func(*RecordedMock) error{transform})
	})
}
