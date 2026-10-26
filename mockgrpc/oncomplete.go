package mockgrpc

import (
	"github.com/rs/zerolog"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
)

var _ vlatte.OnComplete = (*OnComplete)(nil)

// OnComplete implements vlatte.OnComplete for gRPC mocks. Listeners, and the callbacks of [Builder.Callback], get it
// once the mock replied, and the call ends after them. It's read-only: the listeners of a call run at the same time.
// Listeners use the context they get: the client leaving doesn't end it, as it ends the call's.
type OnComplete struct {
	m      *Mock
	call   *Call
	status *status.Status
	kit    *Kit
	args   vlatte.Parameters
	dir    string
	logger *zerolog.Logger
}

// Kind implements vlatte.EventArgs.
func (i *OnComplete) Kind() vlatte.Kind {
	return Kind
}

// Parameters implements vlatte.EventArgs: the parameters of the event, as its listener prepared them.
func (i *OnComplete) Parameters() vlatte.Parameters {
	return i.args
}

// Kit implements vlatte.EventArgs.
func (i *OnComplete) Kit() vlatte.Kit {
	return i.kit
}

// Mock implements vlatte.OnComplete: the mock that answered the call.
func (i *OnComplete) Mock() vlatte.Mock {
	return i.m
}

// Call returns the call the mock answered: its request, and the messages the mock sent, in [Call.Sent].
func (i *OnComplete) Call() *Call {
	return i.call
}

// Status returns the status the mock ended the call with: OK, or the status of its reply.
func (i *OnComplete) Status() *status.Status {
	return i.status
}

// Dir is where relative files of the event's parameters open from: the directory of the mock file that defines the
// event, or "" for the working directory, as for events defined in Go and global events.
func (i *OnComplete) Dir() string {
	return i.dir
}

// Logger is the server's logger, so listeners such as webhooks log where the server does, and as little.
func (i *OnComplete) Logger() *zerolog.Logger {
	return i.logger
}
