package mockgrpc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/caffeine-projects/vlatte"
)

var _ grpc.ServerTransportStream = (*captureStream)(nil)

// recording is a recording session of a server, and what its calls need.
type recording struct {
	session *vlatte.RecordSession[*recordedCall, RecordedRequest, RecordedResponse]
	conf    *RecordConfig
	dir     string
	served  chan struct{} // closed once the call that ended it served what it recorded, or not
	target  *proxyReplier // sends the calls it takes to the server it records, if any

	// stopping is set once a call stops it: the recording takes no more calls, though it still writes.
	stopping atomic.Bool

	// serve is set once a StopRecording asks for what it recorded to be served: the call that ends it serves it.
	serve atomic.Bool

	// ended elects the call that ends it, of those that stop it.
	ended atomic.Bool

	// playErr is why what it recorded isn't served. It is set before served is closed.
	playErr error
}

// StartRecording starts recording what the server proxies as mock files, which answer the same calls the same way:
// see RecordConfig. It starts from the server's record settings, or the defaults, which options change. It returns
// vlatte.ErrRecording while another recording runs, or still writes what it took.
func (app *Server) StartRecording(options ...RecordConfigurer) error {
	conf := defaultRecordConfig()
	if app.config.Record != nil {
		conf = app.config.Record.clone()
	}

	for i, option := range options {
		if err := option.Apply(conf); err != nil {
			return fmt.Errorf("recorder: option at index %d with type %T: %w", i, option, err)
		}
	}

	if err := conf.check(); err != nil {
		return err
	}

	return app.startRecording(conf)
}

// StopRecording stops the recording, waits until it wrote what it took, and returns what it wrote. The server then
// serves the recordings: its mock files load again, with the recording's directory, so the hits of file mocks count
// from 0. When ctx ends first, it returns ctx's error, and the recording goes on writing: a later StopRecording waits
// again, and a StartRecording once it wrote everything ends it. Either serves it. It returns vlatte.ErrNotRecording
// when there is no recording. The error joins the calls and mocks that couldn't be recorded, and why the recordings
// aren't served, such as a mock ID they share with a mock file, or the server closing first.
func (app *Server) StopRecording(ctx context.Context) (*vlatte.RecordResult, error) {
	return app.stopRecording(ctx, true)
}

// RecordingStatus counts the calls of the recording, or returns a zero status when there is none.
func (app *Server) RecordingStatus() vlatte.RecordStatus {
	rec := app.recording.Load()
	if rec == nil {
		return vlatte.RecordStatus{}
	}

	return rec.session.Status()
}

// IsRecording reports whether a recording takes calls.
func (app *Server) IsRecording() bool {
	return app.RecordingStatus().Active
}

// startRecording starts a recording with conf, checked. A recording that stopped, and wrote everything, gives way.
func (app *Server) startRecording(conf *RecordConfig) error {
	// A recording that wrote everything, whose StopRecording gave up waiting, ends here, and is served as it asked.
	if old := app.recording.Load(); old != nil {
		select {
		case <-old.session.Done():
			_, _ = app.end(context.Background(), old, true)
		default:
			return vlatte.ErrRecording
		}
	}

	app.recordMu.Lock()
	defer app.recordMu.Unlock()

	// Another start came first.
	if app.recording.Load() != nil {
		return vlatte.ErrRecording
	}

	rec := &recording{conf: conf, dir: conf.dir(app.config), served: make(chan struct{})}

	if conf.Target != "" {
		target, err := From(conf.Target).DialOptions(conf.DialOptions...).replier()
		if err != nil {
			return fmt.Errorf("recorder: %w", err)
		}

		rec.target = target
	}

	codec := &recordCodec{resolver: app.resolver, headers: conf.RequestHeaders, logger: app.logger}

	session, err := vlatte.NewRecordSession(codec, vlatte.RecordOptions[RecordedRequest, RecordedResponse]{
		Dir:        rec.dir,
		Format:     conf.Format,
		Repeats:    vlatte.RecordFirst,
		Transforms: conf.Transforms,
		Logger:     app.logger,
	})
	if err != nil {
		rec.close()
		return err
	}

	rec.session = session
	app.recording.Store(rec)

	return nil
}

// stopRecording stops the recording, and serves what it recorded when play is set; Close doesn't.
func (app *Server) stopRecording(ctx context.Context, play bool) (*vlatte.RecordResult, error) {
	rec := app.recording.Load()
	if rec == nil {
		return nil, vlatte.ErrNotRecording
	}

	if play {
		rec.serve.Store(true)
	}

	return app.end(ctx, rec, play)
}

// end stops rec. Only here does a recording end: the first call past the stop ends it, and serves what it recorded
// when a StopRecording asked for it, unless mayServe is false, as for Close. The others wait until it is served: a
// StopRecording returns with the recordings served, or why they aren't.
func (app *Server) end(ctx context.Context, rec *recording, mayServe bool) (*vlatte.RecordResult, error) {
	rec.stopping.Store(true)

	res, err := rec.session.Stop(ctx)
	if res == nil {
		return nil, err
	}

	if !rec.ended.CompareAndSwap(false, true) {
		// Served goes first when ctx ended too.
		select {
		case <-rec.served:
		case <-ctx.Done():
			select {
			case <-rec.served:
			default:
				return res, ctx.Err()
			}
		}

		return res, errors.Join(err, rec.playErr)
	}

	rec.close()

	switch {
	case len(res.Recordings) == 0:
	case !mayServe:
		rec.playErr = errors.New("recorder: the server closed before serving the recordings")
	case rec.serve.Load():
		if playErr := app.loader.loadWith(app, rec.dir); playErr != nil {
			rec.playErr = fmt.Errorf("recorder: serving the recordings: %w", playErr)

			// No caller may wait for it, such as when a StartRecording ends a recording whose stop gave up.
			app.logger.Error().Err(rec.playErr).Msg("recorder: the recordings aren't served")
		}
	}

	// Only now: a call that comes meanwhile finds the recording, and waits until it is served.
	app.recording.CompareAndSwap(rec, nil)
	close(rec.served)

	return res, errors.Join(err, rec.playErr)
}

// startConfiguredRecording starts the recording the server's config sets, unless one already runs, such as one
// StartRecording started before the server.
func (app *Server) startConfiguredRecording() error {
	if app.config.Record == nil {
		return nil
	}

	if err := app.startRecording(app.config.Record.clone()); err != nil && !errors.Is(err, vlatte.ErrRecording) {
		return err
	}

	return nil
}

// recordingOf returns the recording that takes call, or nil: one that runs, and whose methods name call's.
func (app *Server) recordingOf(call *Call) *recording {
	rec := app.recording.Load()
	if rec == nil || rec.stopping.Load() {
		return nil
	}

	if len(rec.conf.Methods) > 0 && !slices.ContainsFunc(rec.conf.Methods, func(m string) bool { return namesMethod(m, call.method) }) {
		return nil
	}

	return rec
}

// close releases the connection to the server the recording recorded.
func (rec *recording) close() {
	if rec.target != nil {
		_ = rec.target.Close()
	}
}

// capture captures the answer to a call of ctx for the recording: it returns the context to reply within, whose stream
// keeps the header and trailer the reply sets.
func capture(ctx context.Context) (context.Context, *captureStream) {
	cs := &captureStream{ServerTransportStream: grpc.ServerTransportStreamFromContext(ctx)}
	return grpc.NewContextWithServerTransportStream(ctx, cs), cs
}

// record hands call to the recording, once the client got the whole answer, which ended with err, nil or a status,
// and whose header and trailer cs kept. A mock's answer is recorded only with RecordConfig.IncludeMocked.
func (rec *recording) record(call *Call, cs *captureStream, err error) {
	if !call.proxied && !rec.conf.IncludeMocked {
		return
	}

	header, trailer := cs.kept()

	x := &recordedCall{
		method:    call.method,
		desc:      call.desc,
		header:    call.header,
		requests:  call.Messages(),
		resHeader: header,
		trailer:   trailer,
		status:    status.Convert(err),
	}

	// The call ended with the status of a unary or client streaming answer, or after the messages of a stream.
	switch {
	case call.isServerStream():
		x.messages = call.Sent()
	case err == nil:
		x.messages = []proto.Message{call.answered()}
	}

	rec.session.Record(x, x.size())
}

// captureStream is the stream of a call a recording takes: it keeps the header and trailer the reply sets, as the
// client gets them.
type captureStream struct {
	grpc.ServerTransportStream

	mu      sync.Mutex
	header  metadata.MD
	trailer metadata.MD
}

// SetHeader keeps md, then sets it.
func (s *captureStream) SetHeader(md metadata.MD) error {
	s.keep(&s.header, md)
	return s.ServerTransportStream.SetHeader(md)
}

// SendHeader keeps md, then sends it.
func (s *captureStream) SendHeader(md metadata.MD) error {
	s.keep(&s.header, md)
	return s.ServerTransportStream.SendHeader(md)
}

// SetTrailer keeps md, then sets it.
func (s *captureStream) SetTrailer(md metadata.MD) error {
	s.keep(&s.trailer, md)
	return s.ServerTransportStream.SetTrailer(md)
}

// kept returns copies of the header and trailer the stream kept.
func (s *captureStream) kept() (header, trailer metadata.MD) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.header.Copy(), s.trailer.Copy()
}

// keep adds md to dst, as gRPC adds what is set to what was.
func (s *captureStream) keep(dst *metadata.MD, md metadata.MD) {
	s.mu.Lock()
	defer s.mu.Unlock()

	*dst = metadata.Join(*dst, md)
}
