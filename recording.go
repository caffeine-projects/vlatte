package vlatte

import (
	"errors"

	"github.com/rs/zerolog"
)

var (
	// ErrRecording is returned when a recording starts while another one runs.
	ErrRecording = errors.New("recorder: already recording")

	// ErrNotRecording is returned when there is no recording to stop.
	ErrNotRecording = errors.New("recorder: not recording")
)

// RecordRepeats is what a recording keeps of the calls that repeat a request.
type RecordRepeats int

const (
	// RecordSequence keeps every response, in the order they came: the mock replies with them in turn, then with the
	// last one again. When every response is the same, it keeps one. It is the default, which zero means.
	RecordSequence RecordRepeats = iota + 1

	// RecordFirst keeps the first response only.
	RecordFirst
)

// RecordCodec is what a protocol tells a RecordSession: how a call it captured, X, reads as a mock. Q is the request
// section of the mock, what it matches, and S a response section, what it replies with. Both are written as JSON with
// encoding/json/v2, such as structs with json tags. Calls whose Q writes the same JSON are the same request: they play
// back as one mock, so Q must write equivalent requests the same way.
type RecordCodec[X, Q, S any] interface {
	// Name names the mock of x for people, such as "GET /items?page=2". Its file names start with it.
	Name(x X) string

	// Request returns what a playback of x matches. files places the bodies it holds.
	Request(x X, files *RecordFiles) (Q, error)

	// Response returns what x answered. files places the bodies it holds.
	Response(x X, files *RecordFiles) (S, error)

	// Mock returns the mock file of m, which has one response at least: a value written as JSON, then as the format of
	// the recording.
	Mock(m *RecordedMock[Q, S]) any
}

// RecordedMock is a mock a recording made: a request, and the responses recorded for it, in the order they came.
type RecordedMock[Q, S any] struct {
	// ID names the mock and its file. A transform can't change it.
	ID        string
	Name      string
	Request   Q
	Responses []S
}

// RecordOptions configures a RecordSession.
type RecordOptions[Q, S any] struct {
	// Dir is where the mock files and body files go. It is created when missing.
	Dir string

	// Format is the format of the mock files: json, yaml or yml.
	Format string

	// Repeats is what the recording keeps of a request that comes again. 0 means RecordSequence.
	Repeats RecordRepeats

	// BodyFiles writes every body to a file of its own, instead of inline in the mock file.
	BodyFiles bool

	// MaxInlineBody is how much of a body stays in memory; a larger body goes to a file while it is captured. A text
	// body that stays in memory is written inline in the mock file. 0 means 1 MiB.
	MaxInlineBody int64

	// MaxBodySize bounds the bodies recorded: a call with a larger one isn't recorded. 0 means 1 GiB; a negative
	// value means no limit.
	MaxBodySize int64

	// Transforms change each mock before it is written, in order. A transform that fails skips the mock, and so do
	// transforms that leave it no response.
	Transforms []func(*RecordedMock[Q, S]) error

	Logger *zerolog.Logger
}

// Recording is a mock a recording wrote.
type Recording struct {
	ID   string
	Name string

	// File is the path of the mock file.
	File string

	// BodyFiles are the names of its body files, next to the mock file.
	BodyFiles []string

	// Responses is how many responses it replies with.
	Responses int
}

// RecordResult is what a recording wrote, in the order its requests first came.
type RecordResult struct {
	Recordings []Recording
	Status     RecordStatus
}

// RecordStatus counts the calls of a recording.
type RecordStatus struct {
	// Active is true while the recording takes calls.
	Active bool

	// Calls is how many calls it took.
	Calls int64

	// Mocks is how many mocks it wrote.
	Mocks int64

	// Dropped is how many calls it refused, because too many waited to be written.
	Dropped int64

	// Failed is how many calls and mocks it couldn't record.
	Failed int64
}
