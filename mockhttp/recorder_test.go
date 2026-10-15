package mockhttp

import (
	"crypto/tls"
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/match"
)

func TestRecordConfigApply(t *testing.T) {
	// The receiver holds the user's settings; Apply copies them onto the config being built.
	target := defaultRecordConfig()
	require.NoError(t, (&RecordConfig{SaveFileType: "yaml"}).Apply(target))
	require.Equal(t, "yaml", target.SaveFileType)

	target = defaultRecordConfig()
	require.NoError(t, (&RecordConfig{SaveFileType: ".yaml"}).Apply(target), "should accept extension with dot prefix")
	require.Equal(t, "yaml", target.SaveFileType)

	target = defaultRecordConfig()
	require.NoError(t, (&RecordConfig{}).Apply(target), "should keep the target file type when none is set")
	require.Equal(t, "json", target.SaveFileType)

	require.Error(t, (&RecordConfig{SaveFileType: ".test"}).Apply(defaultRecordConfig()), "unsupported extension")
	require.Error(t, (&RecordConfig{}).Apply(&RecordConfig{}), "no extension defined")
}

func TestRecorder_CreatesSaveDir(t *testing.T) {
	dir := path.Join(t.TempDir(), "nested", "recorded")
	m := MustNew(Setup().Record(RecordDir(dir)))
	m.MustStart()
	m.MustMock(Getf("/rec").Reply(OK()))

	res, err := http.Get(m.URL("/rec"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	// Close waits for the recorder to write every file.
	m.Close()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestRecorder_RepliesWithoutAResponseAreSkippedAndLogged(t *testing.T) {
	// Echo and Handler replies return no response, and dispatch read it (#24). They aren't recorded, and the log
	// says why, instead of a panic in the request log.
	dir := t.TempDir()
	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger).Record(RecordDir(dir)))
	m.MustStart()
	m.MustMock(Getf("/echo").Reply(Echo()))

	status, _ := get(t, m, "/echo")
	require.Equal(t, http.StatusOK, status)

	m.Close()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
	require.Contains(t, logs.String(), "recorder: GET /echo: the reply wrote its own response, which isn't recorded")
	require.NotContains(t, logs.String(), "panic")
}

func TestStopRecordingWithoutRecorder(t *testing.T) {
	m := MustNew()

	require.NotPanics(t, m.StopRecording)
}

func TestRecorder_DispatchAfterStopDoesNotBlock(t *testing.T) {
	m := MustNew(Setup().Record(RecordDir(t.TempDir())))
	m.MustStart()
	t.Cleanup(m.Close)

	m.StopRecording()

	// A request can still be in flight when recording stops; handing it to the recorder must not block.
	req := httptest.NewRequest(http.MethodGet, "/late", nil)
	done := make(chan struct{})
	go func() {
		m.rec.dispatch(req, req.URL, nil, &Response{StatusCode: http.StatusOK})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch blocked after the recorder was stopped")
	}
}

func TestRecordingWithWebProxy(t *testing.T) {
	dir := t.TempDir()

	includedReqHeader := "x-request-excluded"
	excludedReqHeader := "x-req-excluded"

	includedResHeader := "x-response-included"
	excludedResHeader := "x-res-excluded"

	trailer := "final"

	p := MustNew(Setup().
		Name("recorder").
		Proxy().
		Record(
			RecordDir(dir),
			RecordResponseBodyToFile(true),
			RecordRequestHeaders("accept", "content-type", "content-encoding", "content-length", includedReqHeader),
			RecordResponseHeaders("content-type", "content-encoding", "link", "content-length", includedResHeader, trailer),
		))
	p.MustStart()
	scope1 := p.MustMock(Get(match.URLPath("/test")).Reply(Accepted()))

	m := MustNew()
	m.MustStart()
	scope2 := m.MustMock(Get(match.URLPath("/other")).
		Reply(Created().
			Header(includedResHeader, "included").
			Header(excludedResHeader, "excluded").
			Trailer(trailer, "the-trailer-value").
			BodyText("hello world")),
	)

	u, _ := url.Parse(p.URL())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u)}}
	t.Cleanup(client.CloseIdleConnections)

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test", nil)
	res, err := client.Do(req)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusAccepted, res.StatusCode)

	req, _ = http.NewRequest(http.MethodGet, m.URL()+"/other", nil)
	req.Header.Add(excludedReqHeader, "excluded")
	req.Header.Add(includedReqHeader, "included")
	res, err = client.Do(req)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusCreated, res.StatusCode)

	scope1.AssertCalled(t)
	scope2.AssertCalled(t)

	// Close waits for in-flight handlers and for the recorder to write every file.
	p.Close()
	m.Close()

	entries, err := os.ReadDir(dir)

	require.NoError(t, err)
	require.Len(t, entries, 2)

	// Creating a new server that will use the recorded mocks

	srv := MustNew(Setup().MockFilePatterns(dir + "/*mock.json"))
	srv.MustStart()

	defer srv.Close()

	httpClient := &http.Client{}

	req, _ = http.NewRequest(http.MethodGet, srv.URL()+"/other", nil)
	req.Header.Add(excludedReqHeader, "excluded")
	res, err = httpClient.Do(req)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, StatusNoMatch, res.StatusCode)

	req, _ = http.NewRequest(http.MethodGet, srv.URL()+"/other", nil)
	req.Header.Add(includedReqHeader, "included")
	res, err = httpClient.Do(req)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	res.Body.Close()

	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.Equal(t, "hello world", string(b))
	require.Empty(t, res.Header.Get(excludedResHeader))
	require.Equal(t, res.Header.Get(includedResHeader), "included")
	require.Equal(t, res.Header.Get(trailer), "the-trailer-value")
}

func TestRecordingWithWebProxy_CustomRootDir(t *testing.T) {
	dir := t.TempDir()
	actualDir := path.Join(dir, "testdata/_mocks_recorded")

	err := os.MkdirAll(actualDir, os.ModePerm)
	require.NoError(t, err)

	includedReqHeader := "x-request-excluded"
	excludedReqHeader := "x-req-excluded"

	includedResHeader := "x-response-included"
	excludedResHeader := "x-res-excluded"

	trailer := "final"

	p := MustNew(Setup().
		Name("recorder").
		RootDir(dir).
		Proxy().
		Record(
			RecordResponseBodyToFile(true),
			RecordRequestHeaders("accept", "content-type", "content-encoding", "content-length", includedReqHeader),
			RecordResponseHeaders("content-type", "content-encoding", "link", "content-length", includedResHeader, trailer),
		))
	p.MustStart()
	scope1 := p.MustMock(Get(match.URLPath("/test")).Reply(Accepted()))

	m := MustNew()
	m.MustStart()
	scope2 := m.MustMock(Get(match.URLPath("/other")).
		Reply(Created().
			Header(includedResHeader, "included").
			Header(excludedResHeader, "excluded").
			Trailer(trailer, "the-trailer-value").
			BodyText("hello world")),
	)

	u, _ := url.Parse(p.URL())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u)}}
	t.Cleanup(client.CloseIdleConnections)

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test", nil)
	res, err := client.Do(req)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusAccepted, res.StatusCode)

	req, _ = http.NewRequest(http.MethodGet, m.URL()+"/other", nil)
	req.Header.Add(excludedReqHeader, "excluded")
	req.Header.Add(includedReqHeader, "included")
	res, err = client.Do(req)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusCreated, res.StatusCode)

	scope1.AssertCalled(t)
	scope2.AssertCalled(t)

	// Close waits for in-flight handlers and for the recorder to write every file.
	p.Close()
	m.Close()

	entries, err := os.ReadDir(actualDir)

	require.NoError(t, err)
	require.Len(t, entries, 2)

	// Creating a new server that will use the recorded mocks

	srv := MustNew(Setup().MockFilePatterns(actualDir + "/*mock.json"))
	srv.MustStart()

	defer srv.Close()

	httpClient := &http.Client{}

	req, _ = http.NewRequest(http.MethodGet, srv.URL()+"/other", nil)
	req.Header.Add(excludedReqHeader, "excluded")
	res, err = httpClient.Do(req)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, StatusNoMatch, res.StatusCode)

	req, _ = http.NewRequest(http.MethodGet, srv.URL()+"/other", nil)
	req.Header.Add(includedReqHeader, "included")
	res, err = httpClient.Do(req)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	res.Body.Close()

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.Equal(t, "hello world", string(b))
	require.Empty(t, res.Header.Get(excludedResHeader))
	require.Equal(t, res.Header.Get(includedResHeader), "included")
	require.Equal(t, res.Header.Get(trailer), "the-trailer-value")
}

func TestRecordSaveResponseBodyToFile(t *testing.T) {
	dir := t.TempDir()
	file, err := os.Open(path.Join("testdata/recorder/customers.json"))
	require.NoError(t, err)

	defer file.Close()

	data := make([]map[string]any, 0)
	err = json.UnmarshalRead(file, &data)
	require.NoError(t, err)

	target := MustNew(Setup().Name("target"))
	target.MustStart()
	targetScope := target.MustMock(
		Get(match.URLPath("/customers")).Reply(OK().JSON(data).Gzip()),
		Getf("/customers/001").Reply(OK().JSON(data[0]).Gzip()),
		Getf("/customers/002").Reply(OK().JSON(data[1])),
		Postf("/customers").Reply(Created()),
	)

	rec := MustNew(Setup().Name("recorder").Record(
		RecordDir(dir),
		RecordResponseBodyToFile(true),
	))
	rec.MustStart()
	recorderScope := rec.MustMock(AnyMethod().Reply(From(target.URL())))

	httpClient := &http.Client{}

	res, err := httpClient.Get(rec.URL() + "/customers")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)

	b := make([]map[string]any, 0)
	err = json.UnmarshalRead(res.Body, &b)
	require.NoError(t, err)
	require.Equal(t, data, b)

	res.Body.Close()

	res, err = httpClient.Get(rec.URL() + "/customers/001")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, err = httpClient.Get(rec.URL() + "/customers/002")
	require.NoError(t, err)

	b2 := make(map[string]any)
	err = json.UnmarshalRead(res.Body, &b2)
	require.NoError(t, err)

	res.Body.Close()

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, data[1], b2)

	res, err = httpClient.Post(rec.URL()+"/customers", "application/json", nil)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusCreated, res.StatusCode)

	recorderScope.AssertCalled(t)
	targetScope.AssertCalled(t)

	// Close waits for in-flight handlers and for the recorder to write every file.
	rec.Close()
	target.Close()

	entries, err := os.ReadDir(dir)

	require.NoError(t, err)
	require.Len(t, entries, 7)

	// Creating a new server that will use the recorded mocks

	m := MustNew(Setup().MockFilePatterns(dir + "/*mock.json"))
	m.MustStart()

	defer m.Close()

	res, err = httpClient.Get(m.URL() + "/customers")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)

	bbb, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	rb := make([]map[string]any, 0)
	err = json.Unmarshal(bbb, &rb)
	require.NoError(t, err)
	assert.Equal(t, data, rb)

	res.Body.Close()

	res, err = httpClient.Get(m.URL() + "/customers/001")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	assert.Equal(t, http.StatusOK, res.StatusCode)

	res, err = httpClient.Get(m.URL() + "/customers/002")
	require.NoError(t, err)

	rb2 := make(map[string]any)
	err = json.UnmarshalRead(res.Body, &rb2)
	require.NoError(t, err)

	res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, data[1], rb2)

	res, err = httpClient.Post(m.URL()+"/customers", "application/json", nil)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusCreated, res.StatusCode)
}

func TestRecordEmbeddedResponseBodiesYAML(t *testing.T) {
	dir := t.TempDir()
	file, err := os.Open(path.Join("testdata/recorder/customers.json"))
	require.NoError(t, err)

	defer file.Close()

	data := make([]map[string]any, 0)
	err = json.UnmarshalRead(file, &data)
	require.NoError(t, err)

	target := MustNew(Setup().Name("target"))
	target.MustStart()
	targetScope := target.MustMock(
		Get(match.URLPath("/customers")).Reply(OK().JSON(data).Gzip()),
		Getf("/customers/001").Reply(OK().JSON(data[0]).Gzip()),
		Getf("/customers/002").Reply(OK().JSON(data[1])),
		Postf("/customers").Reply(Created()),
	)

	rec := MustNew(Setup().Name("recorder").Record(
		RecordDir(dir),
		RecordResponseBodyToFile(false),
		RecordExtension("yaml"),
	))
	rec.MustStart()
	recorderScope := rec.MustMock(AnyMethod().Reply(From(target.URL())))

	httpClient := &http.Client{}

	res, err := httpClient.Get(rec.URL() + "/customers")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)

	b := make([]map[string]any, 0)
	err = json.UnmarshalRead(res.Body, &b)
	require.NoError(t, err)
	require.Equal(t, data, b)

	res.Body.Close()

	res, err = httpClient.Get(rec.URL() + "/customers/001")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, err = httpClient.Get(rec.URL() + "/customers/002")
	require.NoError(t, err)

	defer res.Body.Close()

	b2 := make(map[string]any)
	err = json.UnmarshalRead(res.Body, &b2)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, data[1], b2)

	res, err = httpClient.Post(rec.URL()+"/customers", "application/json", nil)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	assert.Equal(t, http.StatusCreated, res.StatusCode)

	recorderScope.AssertCalled(t)
	targetScope.AssertCalled(t)

	// Close waits for in-flight handlers and for the recorder to write every file.
	rec.Close()
	target.Close()

	entries, err := os.ReadDir(dir)

	require.NoError(t, err)
	assert.Len(t, entries, 4)

	// Creating a new server that will use the recorded mocks

	m := MustNew(Setup().MockFilePatterns(dir + "/*mock.yaml"))
	m.MustStart()

	defer m.Close()

	res, err = httpClient.Get(m.URL() + "/customers")
	require.NoError(t, err)

	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)

	rb := make([]map[string]any, 0)
	err = json.UnmarshalRead(res.Body, &rb)
	require.NoError(t, err)
	assert.Equal(t, data, rb)

	res, err = httpClient.Get(m.URL() + "/customers/001")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	assert.Equal(t, http.StatusOK, res.StatusCode)

	res, err = httpClient.Get(m.URL() + "/customers/002")
	require.NoError(t, err)

	defer res.Body.Close()

	rb2 := make(map[string]any)
	err = json.UnmarshalRead(res.Body, &rb2)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, data[1], rb2)

	res, err = httpClient.Post(m.URL()+"/customers", "application/json", nil)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	assert.Equal(t, http.StatusCreated, res.StatusCode)
}

func TestRecordTargetTLS(t *testing.T) {
	dir := t.TempDir()
	file, err := os.Open(path.Join("testdata/recorder/customers.json"))
	require.NoError(t, err)

	defer file.Close()

	data := make([]map[string]any, 0)
	err = json.UnmarshalRead(file, &data)
	require.NoError(t, err)

	target := MustNew(Setup().Name("target"))
	target.MustStartTLS()
	targetScope := target.MustMock(
		Get(match.URLPath("/customers")).Reply(OK().JSON(data).Gzip()),
		Getf("/customers/001").Reply(OK().JSON(data[0]).Gzip()),
		Getf("/customers/002").Reply(OK().JSON(data[1])),
		Postf("/customers").Reply(Created()),
	)

	recorder := MustNew(Setup().Name("recorder").Record(
		RecordDir(dir),
		RecordResponseBodyToFile(false),
	))
	recorder.MustStart()
	recorderScope := recorder.MustMock(AnyMethod().Reply(From(target.URL()).SkipSSLVerify()))

	httpClient := &http.Client{}

	res, err := httpClient.Get(recorder.URL() + "/customers")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)

	b := make([]map[string]any, 0)
	err = json.UnmarshalRead(res.Body, &b)
	require.NoError(t, err)
	require.Equal(t, data, b)

	res.Body.Close()

	res, err = httpClient.Get(recorder.URL() + "/customers/001")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, err = httpClient.Get(recorder.URL() + "/customers/002")
	require.NoError(t, err)

	defer res.Body.Close()

	b2 := make(map[string]any)
	err = json.UnmarshalRead(res.Body, &b2)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, data[1], b2)

	res, err = httpClient.Post(recorder.URL()+"/customers", "application/json", nil)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	assert.Equal(t, http.StatusCreated, res.StatusCode)

	recorderScope.AssertCalled(t)
	targetScope.AssertCalled(t)

	// Close waits for in-flight handlers and for the recorder to write every file.
	recorder.Close()
	target.Close()

	entries, err := os.ReadDir(dir)

	require.NoError(t, err)
	assert.Len(t, entries, 4)

	// Creating a new server that will use the recorded mocks

	m := MustNew(Setup().MockFilePatterns(dir + "/*mock.json"))
	m.MustStart()

	defer m.Close()

	res, err = httpClient.Get(m.URL() + "/customers")
	require.NoError(t, err)

	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)

	rb := make([]map[string]any, 0)
	err = json.UnmarshalRead(res.Body, &rb)
	require.NoError(t, err)
	assert.Equal(t, data, rb)

	res, err = httpClient.Get(m.URL() + "/customers/001")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	assert.Equal(t, http.StatusOK, res.StatusCode)

	res, err = httpClient.Get(m.URL() + "/customers/002")
	require.NoError(t, err)

	defer res.Body.Close()

	rb2 := make(map[string]any)
	err = json.UnmarshalRead(res.Body, &rb2)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, data[1], rb2)

	res, err = httpClient.Post(m.URL()+"/customers", "application/json", nil)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	assert.Equal(t, http.StatusCreated, res.StatusCode)
}

func TestRecordTLSClient(t *testing.T) {
	dir := t.TempDir()
	file, err := os.Open(path.Join("testdata/recorder/customers.json"))
	require.NoError(t, err)

	defer file.Close()

	data := make([]map[string]any, 0)
	err = json.UnmarshalRead(file, &data)
	require.NoError(t, err)

	target := MustNew(Setup().Name("target"))
	target.MustStart()
	targetScope := target.MustMock(
		Get(match.URLPath("/customers")).Reply(OK().JSON(data).Gzip()),
		Getf("/customers/001").Reply(OK().JSON(data[0]).Gzip()),
		Getf("/customers/002").Reply(OK().JSON(data[1])),
		Postf("/customers").Reply(Created()),
	)

	recorder := MustNew(Setup().Name("recorder").Record(
		RecordDir(dir),
		RecordResponseBodyToFile(false),
	))
	recorder.MustStartTLS()
	recorderScope := recorder.MustMock(AnyMethod().Reply(From(target.URL())))

	httpClient := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	t.Cleanup(httpClient.CloseIdleConnections)

	res, err := httpClient.Get(recorder.URL() + "/customers")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)

	b := make([]map[string]any, 0)
	err = json.UnmarshalRead(res.Body, &b)
	require.NoError(t, err)
	require.Equal(t, data, b)

	res.Body.Close()

	res, err = httpClient.Get(recorder.URL() + "/customers/001")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, err = httpClient.Get(recorder.URL() + "/customers/002")
	require.NoError(t, err)

	defer res.Body.Close()

	b2 := make(map[string]any)
	err = json.UnmarshalRead(res.Body, &b2)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, data[1], b2)

	res, err = httpClient.Post(recorder.URL()+"/customers", "application/json", nil)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	assert.Equal(t, http.StatusCreated, res.StatusCode)

	recorderScope.AssertCalled(t)
	targetScope.AssertCalled(t)

	// Close waits for in-flight handlers and for the recorder to write every file.
	recorder.Close()
	target.Close()

	entries, err := os.ReadDir(dir)

	require.NoError(t, err)
	assert.Len(t, entries, 4)

	// Creating a new server that will use the recorded mocks

	m := MustNew(Setup().MockFilePatterns(dir + "/*mock.json"))
	m.MustStart()

	defer m.Close()

	res, err = httpClient.Get(m.URL() + "/customers")
	require.NoError(t, err)

	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)

	rb := make([]map[string]any, 0)
	err = json.UnmarshalRead(res.Body, &rb)
	require.NoError(t, err)
	assert.Equal(t, data, rb)

	res, err = httpClient.Get(m.URL() + "/customers/001")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	assert.Equal(t, http.StatusOK, res.StatusCode)

	res, err = httpClient.Get(m.URL() + "/customers/002")
	require.NoError(t, err)

	defer res.Body.Close()

	rb2 := make(map[string]any)
	err = json.UnmarshalRead(res.Body, &rb2)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, data[1], rb2)

	res, err = httpClient.Post(m.URL()+"/customers", "application/json", nil)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	assert.Equal(t, http.StatusCreated, res.StatusCode)
}

func TestRecordBothTLS(t *testing.T) {
	dir := t.TempDir()
	file, err := os.Open(path.Join("testdata/recorder/customers.json"))
	require.NoError(t, err)

	defer file.Close()

	data := make([]map[string]any, 0)
	err = json.UnmarshalRead(file, &data)
	require.NoError(t, err)

	target := MustNew(Setup().Name("target"))
	target.MustStartTLS()
	targetScope := target.MustMock(
		Get(match.URLPath("/customers")).Reply(OK().JSON(data).Gzip()),
		Getf("/customers/001").Reply(OK().JSON(data[0]).Gzip()),
		Getf("/customers/002").Reply(OK().JSON(data[1])),
		Postf("/customers").Reply(Created()),
	)

	recorder := MustNew(Setup().Name("recorder").Record(
		RecordDir(dir),
		RecordResponseBodyToFile(false),
	))
	recorder.MustStartTLS()
	recorderScope := recorder.MustMock(AnyMethod().Reply(From(target.URL()).SkipSSLVerify()))

	httpClient := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	t.Cleanup(httpClient.CloseIdleConnections)

	res, err := httpClient.Get(recorder.URL() + "/customers")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)

	b := make([]map[string]any, 0)
	err = json.UnmarshalRead(res.Body, &b)
	require.NoError(t, err)
	require.Equal(t, data, b)

	res.Body.Close()

	res, err = httpClient.Get(recorder.URL() + "/customers/001")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, err = httpClient.Get(recorder.URL() + "/customers/002")
	require.NoError(t, err)

	defer res.Body.Close()

	b2 := make(map[string]any)
	err = json.UnmarshalRead(res.Body, &b2)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, data[1], b2)

	res, err = httpClient.Post(recorder.URL()+"/customers", "application/json", nil)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	assert.Equal(t, http.StatusCreated, res.StatusCode)

	recorderScope.AssertCalled(t)
	targetScope.AssertCalled(t)

	// Close waits for in-flight handlers and for the recorder to write every file.
	recorder.Close()
	target.Close()

	entries, err := os.ReadDir(dir)

	require.NoError(t, err)
	assert.Len(t, entries, 4)

	// Creating a new server that will use the recorded mocks

	m := MustNew(Setup().MockFilePatterns(dir + "/*mock.json"))
	m.MustStart()

	defer m.Close()

	res, err = httpClient.Get(m.URL() + "/customers")
	require.NoError(t, err)

	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)

	rb := make([]map[string]any, 0)
	err = json.UnmarshalRead(res.Body, &rb)
	require.NoError(t, err)
	assert.Equal(t, data, rb)

	res, err = httpClient.Get(m.URL() + "/customers/001")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	assert.Equal(t, http.StatusOK, res.StatusCode)

	res, err = httpClient.Get(m.URL() + "/customers/002")
	require.NoError(t, err)

	defer res.Body.Close()

	rb2 := make(map[string]any)
	err = json.UnmarshalRead(res.Body, &rb2)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, data[1], rb2)

	res, err = httpClient.Post(m.URL()+"/customers", "application/json", nil)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	assert.Equal(t, http.StatusCreated, res.StatusCode)
}
