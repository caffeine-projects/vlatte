package mockhttp

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// recording is a recording session of a server, and what its calls need.
type recording struct {
	session *vlatte.RecordSession[*recordedCall, RecordedRequest, RecordedResponse]
	codec   *recordCodec
	conf    *RecordConfig
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

	app.recording.CompareAndSwap(rec, nil)

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

	if rec := app.recording.Load(); rec != nil {
		select {
		case <-rec.session.Done():
		default:
			return vlatte.ErrRecording
		}
	}

	codec := newRecordCodec(conf, app.logger)

	session, err := vlatte.NewRecordSession(codec, vlatte.RecordOptions[RecordedRequest, RecordedResponse]{
		Dir:           conf.dir(app.config.RootDir),
		Format:        conf.Format,
		Repeats:       conf.Repeats,
		BodyFiles:     conf.BodyFiles,
		MaxInlineBody: codec.maxInline,
		MaxBodySize:   cmp.Or(conf.MaxBodySize, _defaultMaxBodySize),
		Transforms:    conf.Transforms,
		Logger:        app.logger,
	})
	if err != nil {
		return err
	}

	app.recording.Store(&recording{session: session, codec: codec, conf: conf})

	return nil
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
