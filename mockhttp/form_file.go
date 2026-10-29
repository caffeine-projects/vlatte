package mockhttp

import (
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"slices"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

var (
	_ vlatte.Matcher         = (*fileMatcher)(nil)
	_ vlatte.SelfDescriptive = (*fileMatcher)(nil)
)

// FilePattern is what a file of a multipart form must have to match: its name, content type, headers and content.
// Start one with FileWith, and match it with Builder.FormFile. The files are the ones http.Request.ParseMultipartForm
// reads: parts with a field name and a filename.
type FilePattern struct {
	filename    vlatte.Matcher
	contentType vlatte.Matcher
	header      []namedMatcher
	body        vlatte.Matcher
	bodyFile    string
	every       bool
	err         error
}

// namedMatcher is a matcher of the value with a name, such as a header.
type namedMatcher struct {
	name    string
	matcher vlatte.Matcher
}

// FileWith starts a FilePattern. With no condition set, it matches any file.
func FileWith() *FilePattern {
	return &FilePattern{}
}

// Filename sets a matcher of the file's name, as Go reads it: multipart.FileHeader.Filename, the base of the name sent.
func (p *FilePattern) Filename(m vlatte.Matcher) *FilePattern {
	p.filename = m
	return p
}

// ContentType sets a matcher of the media type of the file's Content-Type: in lower case and without parameters, as
// mime.ParseMediaType reads it, or "" when the file has none.
func (p *FilePattern) ContentType(m vlatte.Matcher) *FilePattern {
	p.contentType = m
	return p
}

// Header adds a matcher of a header of the file's part: its first value, or "" when it has none.
func (p *FilePattern) Header(name string, m vlatte.Matcher) *FilePattern {
	p.header = append(p.header, namedMatcher{name: name, matcher: m})
	return p
}

// Body sets matchers of the file's content, which all must match. The content is text, so an empty file reads "".
// It panics without a matcher.
func (p *FilePattern) Body(matchers ...vlatte.Matcher) *FilePattern {
	switch len(matchers) {
	case 0:
		panic("form_file: Body needs at least one matcher")
	case 1:
		p.body = matchers[0]
	default:
		p.body = match.All(matchers...)
	}

	p.bodyFile = ""

	return p
}

// BodyFile matches a file whose content equals the named file's. The named file is read now: an error reading it
// makes Builder.Build fail.
func (p *FilePattern) BodyFile(filename string) *FilePattern {
	b, err := os.ReadFile(filename)
	if err != nil {
		p.err = err
		return p
	}

	p.bodyFileBytes(filename, b)

	return p
}

// Every makes the pattern match when the field has one file at least, and every one of them matches it.
func (p *FilePattern) Every() *FilePattern {
	p.every = true
	return p
}

// bodyFileBytes sets the content the files must have, read from filename.
func (p *FilePattern) bodyFileBytes(filename string, content []byte) {
	p.body = match.StrictEqual(string(content))
	p.bodyFile = filename
	p.err = nil
}

// FormFile adds a matcher of the files of a field of a multipart form, by the field's name: some file of the field
// must match p, or every one when p is Every. A request without such a file, or without a multipart form, doesn't
// match.
func (b *Builder) FormFile(name string, p *FilePattern) *Builder {
	if p.err != nil && b.err == nil {
		b.err = fmt.Errorf("form_file %s: %w", name, p.err)
	}

	// A copy, so changing p later doesn't change the mock.
	m := &fileMatcher{pattern: *p}
	m.pattern.header = slices.Clone(p.header)

	b.appendExpectation(&expectation{
		Target:   int(matchingScopeFormFile),
		Key:      name,
		Selector: selectFormFiles(name),
		Matcher:  m,
	})

	return b
}

// fileMatcher matches the files of a form field with a FilePattern.
type fileMatcher struct {
	pattern FilePattern
}

// Name implements vlatte.Matcher.
func (m *fileMatcher) Name() string { return "FormFile" }

// Match implements vlatte.Matcher. Its value is the files of a field, as multipart.Form.File holds them.
func (m *fileMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	files, _ := arg.Value().([]*multipart.FileHeader)
	if len(files) == 0 {
		return false, nil
	}

	for _, file := range files {
		ok, err := m.matchFile(arg.Context(), arg.Explain(), file)
		if err != nil {
			return false, err
		}

		if ok != m.pattern.every {
			// Some file matched, or one of every file didn't.
			return ok, nil
		}
	}

	return m.pattern.every, nil
}

// Describe implements vlatte.SelfDescriptive, with the keys of a mock file's form_file pattern.
func (m *fileMatcher) Describe() vlatte.Descriptor {
	props := make(map[string]any)

	if m.pattern.filename != nil {
		props["filename"] = describeMatcher(m.pattern.filename)
	}

	if m.pattern.contentType != nil {
		props["content_type"] = describeMatcher(m.pattern.contentType)
	}

	if len(m.pattern.header) > 0 {
		header := make(map[string]any, len(m.pattern.header))
		for _, h := range m.pattern.header {
			header[h.name] = describeMatcher(h.matcher)
		}

		props["header"] = header
	}

	switch {
	case m.pattern.bodyFile != "":
		props["body_file"] = m.pattern.bodyFile
	case m.pattern.body != nil:
		props["body"] = describeMatcher(m.pattern.body)
	}

	if m.pattern.every {
		props["every"] = true
	}

	return vlatte.NewDescriptor("form_file", props)
}

// matchFile reports whether a file has every condition of the pattern, the cheap ones first.
func (m *fileMatcher) matchFile(ctx context.Context, exp *vlatte.Explanation, file *multipart.FileHeader) (bool, error) {
	conditions := make([]namedMatcher, 0, len(m.pattern.header)+2)
	values := make([]any, 0, len(m.pattern.header)+2)

	if m.pattern.filename != nil {
		conditions = append(conditions, namedMatcher{name: "filename", matcher: m.pattern.filename})
		values = append(values, file.Filename)
	}

	if m.pattern.contentType != nil {
		conditions = append(conditions, namedMatcher{name: "content_type", matcher: m.pattern.contentType})
		values = append(values, mediaType(file.Header.Get(HeaderContentType)))
	}

	for _, h := range m.pattern.header {
		conditions = append(conditions, h)
		values = append(values, file.Header.Get(h.name))
	}

	for i, c := range conditions {
		ok, err := c.matcher.Match(vlatte.NewMatcherArg(ctx, exp, values[i]))
		if err != nil {
			return false, fmt.Errorf("form_file %s: %w", c.name, err)
		}

		if !ok {
			return false, nil
		}
	}

	if m.pattern.body == nil {
		return true, nil
	}

	content, err := fileContent(file)
	if err != nil {
		return false, fmt.Errorf("form_file: reading %s: %w", file.Filename, err)
	}

	ok, err := m.pattern.body.Match(vlatte.NewMatcherArg(ctx, exp, content))
	if err != nil {
		return false, fmt.Errorf("form_file body: %w", err)
	}

	return ok, nil
}

// selectFormFiles selects the files of a form field.
func selectFormFiles(name string) vlatte.Selector[*selectorInput] {
	return func(_ context.Context, r *selectorInput) any { return r.Files[name] }
}

// fileContent reads a file of a form as text.
func fileContent(file *multipart.FileHeader) (string, error) {
	f, err := file.Open()
	if err != nil {
		return "", err
	}

	defer f.Close()

	b, err := io.ReadAll(f)

	return string(b), err
}

// mediaType returns the media type of a Content-Type value, in lower case and without parameters. It is "" when the
// value is empty or doesn't parse.
func mediaType(contentType string) string {
	t, _, _ := mime.ParseMediaType(contentType)
	return t
}

// describeMatcher describes a matcher for a mock file, or as anything when it can't describe itself.
func describeMatcher(m vlatte.Matcher) any {
	if d, ok := vlatte.IsSelfDescriptive(m); ok {
		return d
	}

	return vlatte.DescribeKeyOnly("anything")
}
