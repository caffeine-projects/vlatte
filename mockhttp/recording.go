package mockhttp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/caffeine-projects/vlatte"
)

// recording is a recording session of a server, and what its calls need.
type recording struct {
	session *vlatte.RecordSession[*recordedCall, RecordedRequest, RecordedResponse]
	codec   *recordCodec
	conf    *RecordConfig
	filter  vlatte.Evaluators[*Mock, *selectorInput] // the requests it takes
	target  Replier                                  // sends the requests it takes to the server it records, if any
}

// StartRecording starts recording what the server proxies as mock files, which answer the same requests the same way:
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

// StopRecording stops the recording, waits until it wrote what it took, and returns what it wrote. When ctx ends
// first, it returns ctx's error, and the recording goes on writing: a later StopRecording waits again. It returns
// vlatte.ErrNotRecording when there is no recording. The error joins the calls and mocks that couldn't be recorded.
func (app *Server) StopRecording(ctx context.Context) (*vlatte.RecordResult, error) {
	rec := app.recording.Load()
	if rec == nil {
		return nil, vlatte.ErrNotRecording
	}

	res, err := rec.session.Stop(ctx)
	if res == nil {
		return nil, err
	}

	if app.recording.CompareAndSwap(rec, nil) {
		rec.close()
	}

	return res, err
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
	app.recordMu.Lock()
	defer app.recordMu.Unlock()

	// A recording that wrote everything, whose StopRecording gave up waiting, gives way: it releases its target.
	if old := app.recording.Load(); old != nil {
		select {
		case <-old.session.Done():
			old.close()
		default:
			return vlatte.ErrRecording
		}
	}

	rec := &recording{codec: newRecordCodec(conf, app.logger), conf: conf, filter: conf.filter()}

	if conf.Target != "" {
		target, err := From(conf.Target).SSLVerify(conf.SSLVerify).NewReplier(app)
		if err != nil {
			return fmt.Errorf("recorder: target: %w", err)
		}

		rec.target = target
	}

	session, err := vlatte.NewRecordSession(rec.codec, vlatte.RecordOptions[RecordedRequest, RecordedResponse]{
		Dir:           conf.dir(app.config.RootDir),
		Format:        conf.Format,
		Repeats:       conf.Repeats,
		BodyFiles:     conf.BodyFiles,
		MaxInlineBody: rec.codec.maxInline,
		MaxBodySize:   cmp.Or(conf.MaxBodySize, _defaultMaxBodySize),
		Transforms:    conf.Transforms,
		Logger:        app.logger,
	})
	if err != nil {
		rec.close()
		return err
	}

	rec.session = session
	app.recording.Store(rec)

	return nil
}

// startConfiguredRecording starts the recording the server's config sets, unless one already runs, such as one
// StartRecording started before the server. The config is checked again: its filter may have changed since.
func (app *Server) startConfiguredRecording() error {
	if app.config.Record == nil {
		return nil
	}

	conf := app.config.Record.clone()
	if err := conf.check(); err != nil {
		return err
	}

	if err := app.startRecording(conf); err != nil && !errors.Is(err, vlatte.ErrRecording) {
		return err
	}

	return nil
}

// takes reports whether the recording takes the call of in: whether it matches the recording's filters. A filter
// that fails takes nothing, which is logged: the request is served all the same.
func (rec *recording) takes(ctx context.Context, in *selectorInput) bool {
	if len(rec.filter) == 0 {
		return true
	}

	ok, err := rec.filter.Eval(ctx, vlatte.NewExplanation(), in)
	if err != nil {
		rec.codec.logger.Warn().Err(err).Str("url", in.URL.String()).Msg("recorder: a filter failed: the call isn't recorded")
		return false
	}

	return ok
}

// close releases the connections to the server the recording recorded.
func (rec *recording) close() {
	if c, ok := rec.target.(io.Closer); ok {
		_ = c.Close()
	}
}
