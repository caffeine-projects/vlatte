package mockgrpc

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"google.golang.org/grpc"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
)

var _ RecordConfigurer = (*RecordConfig)(nil)

const (
	_defaultRecordDir    = "_mocks_recorded"
	_defaultRecordFormat = "json"
)

// RecordConfig configures recording: where the recordings go, and what they match. See Server.StartRecording.
//
// A recording writes a mock file for each request: a call of the same method, with the same messages and the
// metadata RequestHeaders names. It answers the way the calls of that request were answered, in turn, then the way
// the last one was, as Repeats sets: its header, its messages, its trailer and its status. A bidi call matches on its
// method and metadata only, as bidi mocks do, and plays its messages back at once, without waiting for the client's.
type RecordConfig struct {
	// Dir is the directory of the recordings. Relative paths start at Config.RootDir. Defaults to _mocks_recorded.
	Dir string

	// Format is the format of the recorded mock files: json, yaml or yml. Defaults to json.
	Format string

	// Target is the server to record, as grpc.NewClient takes it, such as localhost:50051: the calls the recording
	// takes go to it, before any mock, and its answers are recorded. Without one, the recording takes what the mocks
	// that proxy answer, such as From's.
	Target string

	// DialOptions are options of the connection to the Target, after the recording's own, which connects without TLS:
	// such as grpc.WithTransportCredentials for a target that serves TLS.
	DialOptions []grpc.DialOption

	// Methods name the methods of the calls the recording takes, by whole segments of their full method, as Method
	// names them: Method, Service/Method, pkg.Service/Method or /pkg.Service/Method. None takes every method.
	Methods []string

	// RequestHeaders are the metadata keys a recording matches: the value each had, or its absence. A recording matches
	// no other metadata.
	RequestHeaders []string

	// Repeats is what a recording keeps of a request that comes again: every answer, in order, or the first one.
	// Defaults to vlatte.RecordSequence.
	Repeats vlatte.RecordRepeats

	// IncludeMocked records the answers of mocks too. Without it, only proxied answers are recorded: those of the
	// Target and of proxy replies, such as From.
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
	return &RecordConfig{Dir: _defaultRecordDir, Format: _defaultRecordFormat, Repeats: vlatte.RecordSequence}
}

// Apply sets the fields of r that aren't zero on c, and adds r's DialOptions and Transforms to c's, so configs
// compose. A config turns no setting off: options such as RecordIncludeMocked(false) do.
func (r *RecordConfig) Apply(c *RecordConfig) error {
	c.Dir = cmp.Or(r.Dir, c.Dir)
	c.Format = cmp.Or(r.Format, c.Format)
	c.Target = cmp.Or(r.Target, c.Target)
	c.Repeats = cmp.Or(r.Repeats, c.Repeats)
	c.DialOptions = slices.Concat(c.DialOptions, r.DialOptions)
	c.IncludeMocked = c.IncludeMocked || r.IncludeMocked
	c.Transforms = slices.Concat(c.Transforms, r.Transforms)

	if r.Methods != nil {
		c.Methods = slices.Clone(r.Methods)
	}

	if r.RequestHeaders != nil {
		c.RequestHeaders = slices.Clone(r.RequestHeaders)
	}

	return nil
}

// clone returns a copy of r with lists of its own.
func (r *RecordConfig) clone() *RecordConfig {
	c := *r
	c.DialOptions = slices.Clone(r.DialOptions)
	c.Methods = slices.Clone(r.Methods)
	c.RequestHeaders = slices.Clone(r.RequestHeaders)
	c.Transforms = slices.Clone(r.Transforms)

	return &c
}

// check checks the settings, and writes them as the recording reads them: the format as mock files name it, such as
// yaml for .YAML, the default repeats for none, the methods without the spaces around them, and the metadata keys in
// lower case, as gRPC holds them.
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
	}

	var err error
	if r.Methods, err = trimmed("method", r.Methods, strings.TrimSpace); err != nil {
		return err
	}

	r.RequestHeaders, err = trimmed("request header", r.RequestHeaders, func(s string) string {
		return strings.ToLower(strings.TrimSpace(s))
	})

	return err
}

// trimmed returns items as clean writes them, in a slice of its own: the caller's may be shared.
func trimmed(what string, items []string, clean func(string) string) ([]string, error) {
	if items == nil {
		return nil, nil
	}

	out := make([]string, len(items))
	for i, item := range items {
		if out[i] = clean(item); out[i] == "" {
			return nil, fmt.Errorf("recorder: %s %d is empty", what, i)
		}
	}

	return out, nil
}

// dir returns the directory of the recordings of the server c configures: Dir, from c.RootDir when relative.
func (r *RecordConfig) dir(c *Config) string {
	if filepath.IsAbs(r.Dir) || c.RootDir == "" {
		return r.Dir
	}

	return filepath.Join(c.RootDir, r.Dir)
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

// RecordTarget sets the server to record. See RecordConfig.Target.
func RecordTarget(target string) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.Target = target })
}

// RecordDialOptions adds options of the connection to the target. See RecordConfig.DialOptions.
func RecordDialOptions(opts ...grpc.DialOption) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.DialOptions = slices.Concat(c.DialOptions, opts) })
}

// RecordMethods sets the methods of the calls the recording takes. See RecordConfig.Methods.
func RecordMethods(methods ...string) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.Methods = methods })
}

// RecordRequestHeaders sets the metadata keys recordings match: the value each had, or its absence.
func RecordRequestHeaders(keys ...string) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.RequestHeaders = keys })
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
