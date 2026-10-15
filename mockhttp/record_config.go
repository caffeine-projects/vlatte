package mockhttp

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
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

	// RequestHeaders are the request headers a recording matches: the first value each had, or its absence. A
	// recording matches no other header.
	RequestHeaders []string

	// ExcludeResponseHeaders are the response headers a recording leaves out, in any case, such as Set-Cookie.
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
	Repeats vlatte.RecordRepeats

	// IncludeMocked records the answers of mocks too. Without it, only proxied answers are recorded: those of the
	// forward proxy and of proxy replies, such as From.
	IncludeMocked bool

	// Transforms change each recording before it is written, in order. A transform that fails skips the recording.
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
		Dir:           _defaultRecordDir,
		Format:        _defaultRecordFormat,
		MaxInlineBody: _defaultMaxInlineBody,
		MaxBodySize:   _defaultMaxBodySize,
	}
}

// Apply sets the fields of r that aren't zero on c, and adds r's Transforms to c's, so configs compose.
func (r *RecordConfig) Apply(c *RecordConfig) error {
	c.Dir = cmp.Or(r.Dir, c.Dir)
	c.Format = cmp.Or(r.Format, c.Format)
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

	return nil
}

// clone returns a copy of r with lists of its own.
func (r *RecordConfig) clone() *RecordConfig {
	c := *r
	c.RequestHeaders = slices.Clone(r.RequestHeaders)
	c.ExcludeResponseHeaders = slices.Clone(r.ExcludeResponseHeaders)
	c.Transforms = slices.Clone(r.Transforms)

	return &c
}

// check checks the settings, and writes the format as mock files name it, such as yaml for .YAML.
func (r *RecordConfig) check() error {
	format, ok := config.Format(r.Format)
	if !ok {
		return fmt.Errorf("recorder: format %q is not one of %s", r.Format, strings.Join(MockFileFormats(), ", "))
	}

	r.Format = format

	switch {
	case r.Dir == "":
		return errors.New("recorder: no directory to record into")
	case r.Repeats != vlatte.RecordSequence && r.Repeats != vlatte.RecordFirst:
		return fmt.Errorf("recorder: repeats %d is not vlatte.RecordSequence or vlatte.RecordFirst", r.Repeats)
	case r.MaxInlineBody < 0:
		return fmt.Errorf("recorder: MaxInlineBody must be 0 or more, not %d", r.MaxInlineBody)
	}

	return nil
}

// dir returns the directory of the recordings: Dir, from root when it is relative.
func (r *RecordConfig) dir(root string) string {
	if filepath.IsAbs(r.Dir) {
		return r.Dir
	}

	return filepath.Join(root, r.Dir)
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

// RecordExcludeResponseHeaders sets the response headers recordings leave out, in any case.
func RecordExcludeResponseHeaders(names ...string) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.ExcludeResponseHeaders = names })
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
