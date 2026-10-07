package mockhttp

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte/internal/config"
)

var _ RecordConfigurer = (*RecordConfig)(nil)

var _regexNoSpecialCharacters = regexp.MustCompile("[^a-z0-9]")

type recorder struct {
	logger  *zerolog.Logger
	rootDir string
	active  atomic.Bool
	config  *RecordConfig
	in      chan recArgs
	close   chan struct{}
	mu      sync.Mutex
}

// RecordConfig configures HTTP request and response recording.
type RecordConfig struct {
	// RequestHeaders define the request headers that should be recorded.
	// Defaults to: "accept", "content-type"
	RequestHeaders []string

	// ResponseHeaders define the response headers that should be recorded.
	// Defaults to:
	//	"content-type"
	//	"link"
	//	"content-length"
	//	"cache-control"
	//	"retry-after"
	ResponseHeaders []string

	// SaveDir defines the directory to save the recorded mocks.
	SaveDir string

	// SaveFileType is the format of the recorded mock files: json, yaml or yml. Defaults to json.
	SaveFileType string

	// SaveResponseBodyToFile defines if the recorded body should be saved to a separate file.
	// Defaults to false, embedding the response body into the mock definition file.
	SaveResponseBodyToFile bool
}

// RecordConfigurer lets users configure recording.
type RecordConfigurer interface {
	Apply(c *RecordConfig) error
}

type recordConfigFunc func(config *RecordConfig)

func (f recordConfigFunc) Apply(config *RecordConfig) error {
	f(config)
	return nil
}

// Apply allows using RecordConfig as RecordConfigurer.
func (r *RecordConfig) Apply(opts *RecordConfig) error {
	fileType := r.SaveFileType
	if fileType == "" {
		fileType = opts.SaveFileType
	}

	extension, err := recordFormat(fileType)
	if err != nil {
		return err
	}

	opts.SaveFileType = extension
	opts.SaveDir = r.SaveDir
	opts.SaveResponseBodyToFile = r.SaveResponseBodyToFile
	opts.ResponseHeaders = r.ResponseHeaders
	opts.RequestHeaders = r.RequestHeaders

	return nil
}

func defaultRecordConfig() *RecordConfig {
	return &RecordConfig{
		SaveDir:                "testdata/_mocks_recorded",
		SaveFileType:           "json",
		SaveResponseBodyToFile: false,
		RequestHeaders:         []string{"accept", "content-type", "content-encoding", "content-length"},
		ResponseHeaders:        []string{"content-type", "content-encoding", "link", "content-length", "cache-control", "retry-after"},
	}
}

// Recording data transfer structs.
type recArgs struct {
	request  recRequest
	response recResponse
}

type recRequest struct {
	uri    string // names the file
	path   string // matches the request: decoded, without the query, which query matches
	method string
	header http.Header
	query  url.Values
	body   []byte
}

type recResponse struct {
	status int
	header http.Header
	body   []byte
}

// newRecorder checks the format of the recorded files, whatever set it, then returns a recorder.
func newRecorder(app *Server, conf *RecordConfig) (*recorder, error) {
	format, err := recordFormat(conf.SaveFileType)
	if err != nil {
		return nil, err
	}

	conf.SaveFileType = format

	return &recorder{config: conf, logger: app.logger, rootDir: app.config.RootDir, close: make(chan struct{})}, nil
}

// recordFormat normalizes the format of recorded mock files, such as ".YAML", and checks that mock files can use it.
func recordFormat(fileType string) (string, error) {
	format, ok := mockFileFormat(fileType)
	if !ok {
		return "", fmt.Errorf("recorder: recorded mock file format %q is not supported: use %s",
			fileType, strings.Join(MockFileFormats(), ", "))
	}

	return format, nil
}

func (r *recorder) start() {
	r.mu.Lock()

	if r.active.Load() {
		r.mu.Unlock()
		return
	}

	r.in = make(chan recArgs)
	r.active.Store(true)

	r.mu.Unlock()

	go func() {
		for {
			select {
			case a, ok := <-r.in:
				if !ok {
					return
				}

				err := r.process(a)
				if err != nil {
					r.logger.Error().Err(err).Msgf("%s", err.Error())
				}

			case <-r.close:
				r.stop()
				return
			}
		}
	}()
}

func (r *recorder) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.active.Load() {
		return
	}

	r.close <- struct{}{}
	r.active.Store(false)
}

func (r *recorder) isActive() bool {
	return r.active.Load()
}

func (r *recorder) dispatch(req *http.Request, parsedURL *url.URL, rawReqBody []byte, res *Response) {
	// stop holds r.mu while it signals the worker, so checking and sending under r.mu is atomic.
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.active.Load() {
		return
	}

	r.in <- recArgs{
		request:  recRequest{parsedURL.RequestURI(), parsedURL.Path, req.Method, req.Header.Clone(), req.URL.Query(), rawReqBody},
		response: recResponse{res.StatusCode, res.Header.Clone(), res.Body},
	}
}

// process writes the mock file of a recorded request and response.
func (r *recorder) process(arg recArgs) error {
	request := map[string]any{"path": arg.request.path, "method": arg.request.method}
	response := map[string]any{"status": int64(arg.response.status)}

	requestHeaders := make(map[string]any, len(r.config.RequestHeaders))
	for _, h := range r.config.RequestHeaders {
		if v := arg.request.header.Get(h); v != "" {
			requestHeaders[h] = v
		}
	}

	// Content-Length isn't recorded: the replaying server computes it for the body it sends,
	// which differs from the recorded one when it compresses the body again.
	responseHeaders := make(map[string]any, len(r.config.ResponseHeaders))
	for _, h := range r.config.ResponseHeaders {
		if v := arg.response.header.Get(h); v != "" && !strings.EqualFold(h, HeaderContentLength) {
			responseHeaders[h] = v
		}
	}

	query := make(map[string]any, len(arg.request.query))
	for k := range arg.request.query {
		query[k] = arg.request.query.Get(k)
	}

	request["query"] = query
	request["header"] = requestHeaders

	requestBodyHash := ""
	if len(arg.request.body) > 0 {
		request["body"] = []any{"equal", string(arg.request.body)}

		h := fnv.New64()
		h.Write(arg.request.body)
		requestBodyHash = hex.EncodeToString(h.Sum(nil))
	}

	nm := strings.ReplaceAll(arg.request.uri, " ", "-")
	nm = strings.TrimPrefix(_regexNoSpecialCharacters.ReplaceAllString(nm, "-"), "-")
	if requestBodyHash != "" {
		nm += "--" + requestBodyHash
	}

	saveDir := r.config.SaveDir
	if !filepath.IsAbs(saveDir) {
		saveDir = filepath.Join(r.rootDir, saveDir)
	}

	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		return err
	}

	name := fmt.Sprintf("%s-%s", arg.request.method, nm)
	mockFile := filepath.Join(saveDir, fmt.Sprintf("%s.mock.%s", name, r.config.SaveFileType))

	if len(arg.response.body) > 0 {
		body := arg.response.body
		if arg.response.header.Get(HeaderContentEncoding) == "gzip" {
			gz, err := gzip.NewReader(bytes.NewReader(arg.response.body))
			if err != nil {
				return err
			}

			defer gz.Close()

			if body, err = io.ReadAll(gz); err != nil {
				return err
			}

			// The recording holds the body as it reads, which encoding compresses again when it replays.
			// encoding also sets Content-Encoding; recorded too, it would repeat.
			response["encoding"] = "gzip"

			for h := range responseHeaders {
				if strings.EqualFold(h, HeaderContentEncoding) {
					delete(responseHeaders, h)
				}
			}
		}

		// A mock file is text, so a body that isn't goes to a file of its own, named after its content type.
		if r.config.SaveResponseBodyToFile || !utf8.Valid(body) {
			contentType, _, _ := strings.Cut(arg.response.header.Get(HeaderContentType), ";")

			bodyFile := name + "--response-body"
			if ext, _ := mime.ExtensionsByType(strings.TrimSpace(contentType)); len(ext) > 0 {
				bodyFile += ext[0]
			} else {
				bodyFile += ".bin"
			}

			if err := os.WriteFile(filepath.Join(saveDir, bodyFile), body, 0o644); err != nil {
				return err
			}

			// The body file sits next to the mock file, which is where a relative body_file opens from.
			response["body_file"] = bodyFile
		} else {
			response["body"] = string(body)
		}
	}

	response["header"] = responseHeaders

	data, err := config.Encode(map[string]any{"request": request, "response": response}, r.config.SaveFileType)
	if err != nil {
		return err
	}

	// A mock file is a Go template first, so each {{ of the recording is written as an action that prints it.
	data = bytes.ReplaceAll(data, []byte("{{"), []byte("{{`{{`}}"))

	return os.WriteFile(mockFile, data, 0o644)
}

//
// Recording Configuration Functions.
//

// RecordRequestHeaders define the request headers that should be recorded.
func RecordRequestHeaders(h ...string) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.RequestHeaders = h })
}

// RecordResponseHeaders define the response headers that should be recorded.
func RecordResponseHeaders(h ...string) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.ResponseHeaders = h })
}

// RecordDir defines the directory to save the recorded mocks.
func RecordDir(dir string) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.SaveDir = dir })
}

// RecordExtension sets the format of the recorded mock files: json, yaml or yml. Defaults to json.
func RecordExtension(ext string) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.SaveFileType = ext })
}

// RecordResponseBodyToFile defines if the recorded body should be saved to separate file.
// Defaults to false, embedding the response body into the mock definition file.
func RecordResponseBodyToFile(v bool) RecordConfigurer {
	return recordConfigFunc(func(c *RecordConfig) { c.SaveResponseBodyToFile = v })
}
