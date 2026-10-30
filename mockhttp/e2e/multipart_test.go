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

	"github.com/caffeine-projects/vlatte"
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

func TestMultipart_AByteBodyLoadsBackFromItsDescription(t *testing.T) {
	// JSON and YAML can't hold bytes that aren't UTF-8 as text: the description of a part built in Go with such a
	// body, as the admin API serves it, failed to encode. A templated body writes its byte bodies as they are: bytes
	// that are UTF-8 must not load back as a template. Both describe in body_base64, and serve the same bytes back.
	png := []byte{0x89, 'P', 'N', 'G', 0xff, 0xfe, 0x00}

	tests := []struct {
		name string
		mock vlatte.Builder[*mockhttp.Mock, *mockhttp.Server]
		want string
	}{
		{
			name: "built in Go, not UTF-8",
			mock: mockhttp.Getf("/x").ID("x").Reply(mockhttp.OK().
				Multipart(mockhttp.MultipartMixed(mockhttp.NewPart().ContentType("image/png").Body(png)).Boundary("b"))),
			want: string(png),
		},
		{
			name: "from a file, templated",
			mock: mockhttp.FromBytes([]byte("id: x\nrequest: {method: GET, path: /x}\nresponse:\n  template: {enabled: true}\n"+
				"  multipart: {type: mixed, boundary: b, parts: [{body_base64: e3sgLlggfX0gYXMgaXM=}]}\n"), "yaml"),
			want: "{{ .X }} as is",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			serve := func(b vlatte.Builder[*mockhttp.Mock, *mockhttp.Server]) (string, *mockhttp.Mock) {
				m := mockhttp.MustNew(mockhttp.Setup().Silent())
				m.MustStart()
				t.Cleanup(m.Close)

				scope := m.MustMock(b)

				res, err := http.Get(m.URL("/x"))
				require.NoError(t, err)

				body, err := io.ReadAll(res.Body)
				require.NoError(t, err)
				require.NoError(t, res.Body.Close())
				require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)

				_, _, content := parts(t, res, body)
				require.Len(t, content, 1)

				return string(content[0]), scope.Mock("x")
			}

			got, mock := serve(tc.mock)
			require.Equal(t, tc.want, got)

			doc, err := mockhttp.EncodeMock(mock, "json")
			require.NoError(t, err)
			require.Contains(t, string(doc), "body_base64")

			again, _ := serve(mockhttp.FromBytes(doc, "json"))
			require.Equal(t, tc.want, again, "%s", doc)
		})
	}
}

func TestMultipart_RepeatedHeaderConstraintsSurviveExport(t *testing.T) {
	// Saving and loading a mock must preserve every constraint on a part header, including names in different cases.
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			for _, secondHeader := range []string{"X-Tag", "x-tag"} {
				t.Run(secondHeader, func(t *testing.T) {
					m := mockhttp.MustNew(mockhttp.Setup().Silent())
					m.MustStart()
					t.Cleanup(m.Close)

					scope := m.MustMock(mockhttp.Postf("/upload").ID("upload").
						FormFile("avatar", mockhttp.FileWith().
							Header("X-Tag", match.HasPrefix("required-")).
							Header(secondHeader, match.HasSuffix("-suffix"))).
						Reply(mockhttp.OK()))

					check := func(t *testing.T) {
						tests := []struct {
							value  string
							status int
						}{
							{value: "required-value-suffix", status: http.StatusOK},
							{value: "wrong-suffix", status: mockhttp.StatusNoMatch},
							{value: "required-value", status: mockhttp.StatusNoMatch},
						}

						for _, tc := range tests {
							t.Run(tc.value, func(t *testing.T) {
								var body bytes.Buffer
								w := multipart.NewWriter(&body)
								part, err := w.CreatePart(textproto.MIMEHeader{
									"Content-Disposition": {multipart.FileContentDisposition("avatar", "pic.png")},
									"X-Tag":               {tc.value},
								})
								require.NoError(t, err)
								_, err = part.Write([]byte("hello"))
								require.NoError(t, err)
								require.NoError(t, w.Close())

								res, got := postBody(t, m.URL("/upload"), w.FormDataContentType(), body.Bytes())
								require.Equal(t, tc.status, res.StatusCode, "%s", got)
							})
						}
					}

					t.Run("original", check)

					doc, err := mockhttp.EncodeMock(scope.Mock("upload"), format)
					require.NoError(t, err)
					require.NoError(t, m.Unmock(t.Context(), scope))
					_, err = m.Mock(mockhttp.FromBytes(doc, format))
					require.NoError(t, err)

					t.Run("reloaded", check)
				})
			}
		})
	}
}

func TestMultipart_RepeatedCustomHeaderMatchersRemainExportable(t *testing.T) {
	// A custom matcher without a description keeps the existing anything fallback, even when its header is repeated.
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			m := mockhttp.MustNew(mockhttp.Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)

			custom := vlatte.MatcherFunc("custom", func(vlatte.MatcherArg) (bool, error) { return true, nil })
			scope := m.MustMock(mockhttp.Postf("/upload").ID("upload").
				FormFile("avatar", mockhttp.FileWith().
					Header("Content-Type", custom).
					Header("Content-Type", custom)).
				Reply(mockhttp.OK()))

			doc, err := mockhttp.EncodeMock(scope.Mock("upload"), format)
			require.NoError(t, err)
			_, err = m.Remock(t.Context(), scope, mockhttp.FromBytes(doc, format))
			require.NoError(t, err)

			contentType, body := upload(t, "pic.png", []byte("hello"))
			res, got := postBody(t, m.URL("/upload"), contentType, body)
			require.Equal(t, http.StatusOK, res.StatusCode, "%s", got)
		})
	}
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
