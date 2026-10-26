package e2e

import (
	"bytes"
	"compress/gzip"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

// upload writes a browser-like form: a title field and an avatar file.
func upload(t *testing.T, filename string, avatar []byte) (string, []byte) {
	t.Helper()

	var buf bytes.Buffer

	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("title", "Holiday"))

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", multipart.FileContentDisposition("avatar", filename))
	h.Set("Content-Type", "image/png")

	part, err := w.CreatePart(h)
	require.NoError(t, err)

	_, err = part.Write(avatar)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	return w.FormDataContentType(), buf.Bytes()
}

func postBody(t *testing.T, url, contentType string, body []byte) (*http.Response, []byte) {
	t.Helper()

	res, err := http.Post(url, contentType, bytes.NewReader(body))
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return res, b
}

// parts reads a multipart response as a client does.
func parts(t *testing.T, res *http.Response, body []byte) (map[string]string, []*multipart.Part, [][]byte) {
	t.Helper()

	_, params, err := mime.ParseMediaType(res.Header.Get(mockhttp.HeaderContentType))
	require.NoError(t, err)

	var (
		got     []*multipart.Part
		content [][]byte
	)

	r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		p, err := r.NextPart()
		if errors.Is(err, io.EOF) {
			return params, got, content
		}

		require.NoError(t, err)

		b, err := io.ReadAll(p)
		require.NoError(t, err)

		got = append(got, p)
		content = append(content, b)
	}
}

func TestMultipart_MatchesAnUpload(t *testing.T) {
	// A browser-like upload matches a mock from Go and one from a mock file, which compares the file with a body
	// file byte for byte. Another filename, or other bytes, don't match.
	avatar, err := os.ReadFile("testdata/multipart/avatar.png")
	require.NoError(t, err)

	m := mockhttp.MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	fromFile := m.MustMock(mockhttp.FromFile("testdata/multipart/upload.yaml"))
	fromGo := m.MustMock(mockhttp.Postf("/upload-go").
		FormField("title", match.Eq("Holiday")).
		FormFile("avatar", mockhttp.FileWith().Filename(match.HasSuffix(".png")).ContentType(match.Eq("image/png"))).
		Reply(mockhttp.Created()))

	contentType, body := upload(t, "avatar.png", avatar)

	res, got := postBody(t, m.URL("/upload"), contentType, body)
	require.Equal(t, http.StatusCreated, res.StatusCode, "%s", got)
	require.JSONEq(t, `{"id": 1}`, string(got))

	res, _ = postBody(t, m.URL("/upload-go"), contentType, body)
	require.Equal(t, http.StatusCreated, res.StatusCode)

	otherName, otherNameBody := upload(t, "avatar.jpg", avatar)
	res, _ = postBody(t, m.URL("/upload"), otherName, otherNameBody)
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode, "another filename")

	res, _ = postBody(t, m.URL("/upload-go"), otherName, otherNameBody)
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode, "another filename")

	otherBytes, otherBytesBody := upload(t, "avatar.png", append(avatar, '!'))
	res, _ = postBody(t, m.URL("/upload"), otherBytes, otherBytesBody)
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode, "other bytes")

	fromFile.AssertNumberOfCalls(t, 1)
	fromGo.AssertNumberOfCalls(t, 1)
}

func TestMultipart_FormFieldsInEitherEncoding(t *testing.T) {
	// A form field matches in a urlencoded and a multipart form alike, as http.Request.PostFormValue reads both.
	m := mockhttp.MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(mockhttp.Postf("/form").FormField("title", match.Eq("Holiday")).Reply(mockhttp.OK()))

	contentType, body := upload(t, "a.png", []byte("x"))

	res, _ := postBody(t, m.URL("/form"), contentType, body)
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, _ = postBody(t, m.URL("/form"), mockhttp.MIMEFormURLEncoded, []byte(url.Values{"title": {"Holiday"}}.Encode()))
	require.Equal(t, http.StatusOK, res.StatusCode)

	scoped.AssertNumberOfCalls(t, 2)
}

func TestMultipart_RelatedReplyFromAFile(t *testing.T) {
	// A multipart/related reply from a mock file: the parameters of its Content-Type, a JSON root part, and an image
	// read from a body file, all gzipped.
	avatar, err := os.ReadFile("testdata/multipart/avatar.png")
	require.NoError(t, err)

	m := mockhttp.MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.FromFile("testdata/multipart/related.yaml"))

	req, err := http.NewRequest(http.MethodGet, m.URL("/related"), nil)
	require.NoError(t, err)

	// Without the transport's own gunzip, to see the encoding.
	res, err := (&http.Transport{DisableCompression: true}).RoundTrip(req)
	require.NoError(t, err)

	t.Cleanup(func() { res.Body.Close() })

	require.Equal(t, "gzip", res.Header.Get(mockhttp.HeaderContentEncoding))

	gz, err := gzip.NewReader(res.Body)
	require.NoError(t, err)

	body, err := io.ReadAll(gz)
	require.NoError(t, err)

	params, got, content := parts(t, res, body)
	require.Equal(t, "<root>", params["start"])
	require.Equal(t, "application/json", params["type"])
	require.Len(t, got, 2)

	require.Equal(t, "<root>", got[0].Header.Get("Content-Id"))
	require.Equal(t, "application/json", got[0].Header.Get("Content-Type"))
	require.JSONEq(t, `{"id": 1, "logo": "cid:logo"}`, string(content[0]))

	require.Equal(t, "<logo>", got[1].Header.Get("Content-Id"))
	require.Equal(t, "logo.png", got[1].FileName())
	require.Equal(t, "application/octet-stream", got[1].Header.Get("Content-Type"))
	require.Equal(t, avatar, content[1])
}

func TestMultipart_SequenceOfMultipartReplies(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.FromFile("testdata/multipart/sequence.yaml"))

	for _, want := range []string{"first", "second"} {
		res, err := http.Get(m.URL("/sequence"))
		require.NoError(t, err)

		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		_, _, content := parts(t, res, body)
		require.Equal(t, want, string(content[0]))
	}
}

func TestMultipart_TemplatedPartEchoesTheUpload(t *testing.T) {
	// A templated part reads the uploaded file, as net/http parsed it.
	m := mockhttp.MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.FromFile("testdata/multipart/echo.yaml"))

	contentType, body := upload(t, "me.png", []byte("12345"))

	res, got := postBody(t, m.URL("/echo"), contentType, body)
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", got)

	_, _, content := parts(t, res, got)
	require.Equal(t, "me.png: 5 bytes", string(content[0]))
}

func TestMultipart_AdminRoundTrip(t *testing.T) {
	// A mock with a form_file pattern and a multipart reply, posted to the admin API as JSON, is stored, shown and
	// served.
	m := mockhttp.MustNew(mockhttp.Setup().Admin(admin.MustNew()))
	m.MustStart()
	t.Cleanup(m.Close)

	doc := `{
		"id": "upload",
		"request": {"method": "POST", "path": "/admin-upload", "form_file": {"avatar": {"filename": "a.png"}}},
		"response": {"status": 200, "multipart": {"type": "mixed", "boundary": "b", "parts": [{"body": "ok"}]}}
	}`

	res, err := http.Post(m.AdminURL()+"/mocks", mockhttp.MIMEApplicationJSON, strings.NewReader(doc))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusCreated, res.StatusCode)

	res, err = http.Get(m.AdminURL() + "/mocks/upload")
	require.NoError(t, err)

	var shown map[string]any
	require.NoError(t, json.UnmarshalRead(res.Body, &shown))
	require.NoError(t, res.Body.Close())

	require.Equal(t, map[string]any{"avatar": map[string]any{"filename": []any{"eq", "a.png"}}}, shown["request"].(map[string]any)["form_file"])
	require.Equal(t, map[string]any{"type": "mixed", "boundary": "b", "parts": []any{map[string]any{"body": "ok"}}}, shown["response"].(map[string]any)["multipart"])

	contentType, body := upload(t, "a.png", []byte("x"))

	res, got := postBody(t, m.URL("/admin-upload"), contentType, body)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "multipart/mixed; boundary=b", res.Header.Get(mockhttp.HeaderContentType))
	require.Equal(t, "--b\r\n\r\nok\r\n--b--\r\n", string(got))
}

func TestMultipart_StreamsItsParts(t *testing.T) {
	// Decision 14, over real time: a part's delay is waited after the delimiter line that opens it, and what comes
	// before is flushed first. So a client reads each part as it comes: none before its delay, and the first well
	// before the last, as an x-mixed-replace camera feed needs.
	m := mockhttp.MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	const delay = 150 * time.Millisecond

	m.MustMock(mockhttp.Getf("/stream").Reply(mockhttp.OK().Multipart(mockhttp.NewMultipart("x-mixed-replace",
		mockhttp.NewPart().ContentType("text/plain").BodyText("frame 1"),
		mockhttp.NewPart().ContentType("text/plain").BodyText("frame 2").Delay(delay),
		mockhttp.NewPart().ContentType("text/plain").BodyText("frame 3").Delay(delay),
	))))

	start := time.Now()

	res, err := http.Get(m.URL("/stream"))
	require.NoError(t, err)

	t.Cleanup(func() { res.Body.Close() })

	mediaType, params, err := mime.ParseMediaType(res.Header.Get(mockhttp.HeaderContentType))
	require.NoError(t, err)
	require.Equal(t, "multipart/x-mixed-replace", mediaType)

	r := multipart.NewReader(res.Body, params["boundary"])

	arrived := make([]time.Duration, 0, 3)
	for i := range 3 {
		part, err := r.NextPart()
		require.NoError(t, err)

		b, err := io.ReadAll(part)
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("frame %d", i+1), string(b))

		arrived = append(arrived, time.Since(start))
	}

	require.GreaterOrEqual(t, arrived[1], delay, "frame 2 waits its delay")
	require.GreaterOrEqual(t, arrived[2], 2*delay, "frame 3 waits both delays")
	require.Less(t, arrived[0], arrived[2]-100*time.Millisecond, "frame 1 arrives before frame 3 is written: it was flushed")
}
