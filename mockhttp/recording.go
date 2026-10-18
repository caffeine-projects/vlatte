package mockhttp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/caffeine-projects/vlatte"
)

// recording is a recording session of a server, and what its calls need.
type recording struct {
	session *vlatte.RecordSession[*recordedCall, RecordedRequest, RecordedResponse]
	codec   *recordCodec
	conf    *RecordConfig
	dir     string
	served  chan struct{}                            // closed once the call that ended it served what it recorded, or not
	filter  vlatte.Evaluators[*Mock, *selectorInput] // the requests it takes
	target  Replier                                  // sends the requests it takes to the server it records, if any

	// stopping is set once a call stops it: the recording takes no more requests, though it still writes.
	stopping atomic.Bool

	// serve is set once a StopRecording asks for what it recorded to be served: the call that ends it serves it.
	serve atomic.Bool

	// ended elects the call that ends it, of those that stop it.
	ended atomic.Bool

	// playErr is why what it recorded isn't served. It is set before served is closed.
	playErr error
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

	rec := &recording{
		codec:  newRecordCodec(conf, app.config.HeaderNamesToRedact, app.logger),
		conf:   conf,
		dir:    conf.dir(app.config.RootDir),
		served: make(chan struct{}),
		filter: conf.filter(),
	}

	if conf.Target != "" {
		target, err := From(conf.Target).SSLVerify(!conf.SkipSSLVerify).NewReplier(app)
		if err != nil {
			return fmt.Errorf("recorder: target: %w", err)
		}

		rec.target = target
	}

	session, err := vlatte.NewRecordSession(rec.codec, vlatte.RecordOptions[RecordedRequest, RecordedResponse]{
		Dir:           rec.dir,
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
// StopRecording returns with the recordings served, or why they aren't, and Close doesn't close the mocks while they
// load.
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
		if playErr := app.play(rec.dir); playErr != nil {
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

// play serves the recordings of dir with the mock files, which load again. When they fail to load, the server
// serves what it served before.
func (app *Server) play(dir string) error {
	app.recordMu.Lock()
	last := app.played
	app.played = dir
	app.recordMu.Unlock()

	if err := app.files.Load(app); err != nil {
		app.recordMu.Lock()
		app.played = last
		app.recordMu.Unlock()

		return err
	}

	return nil
}

// recordDirs returns the directories whose recordings the server serves: the one its config records into, and the
// one of the last recording StopRecording ended.
func (app *Server) recordDirs() []string {
	dirs := make([]string, 0, 2)
	if app.config.Record != nil {
		dirs = append(dirs, app.config.Record.dir(app.config.RootDir))
	}

	app.recordMu.Lock()
	defer app.recordMu.Unlock()

	if app.played != "" {
		dirs = append(dirs, app.played)
	}

	return dirs
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
