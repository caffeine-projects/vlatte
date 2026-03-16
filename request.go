package vlatte

import "time"

type Req[R any] interface {
	ID() string
	StartedAt() time.Time
	OriginalRequest() R
	RawBody() []byte
	ParsedBody() any
}

type RequestParser[R any, O any] interface {
	Parse(r R, options O) (Req[R], []byte, error)
}
