package mockhttp_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

// The docs site shows Example from here to the end of the region.
// #region example

func Example() {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	defer m.Close()

	scoped := m.MustMock(mockhttp.Getf("/hello").Reply(mockhttp.OK().PlainText("hello world")))

	res, err := http.Get(m.URL("/hello"))
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}

	fmt.Println(res.StatusCode, string(body), scoped.Called())
	// Output: 200 hello world true
}

// #endregion example

func Example_multipart() {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	defer m.Close()

	// Match an upload by its fields and files, and reply with a multipart body.
	m.MustMock(mockhttp.Postf("/upload").
		FormField("title", match.Equal("Holiday")).
		FormFile("photo", mockhttp.FileWith().Filename(match.HasSuffix(".png"))).
		Reply(mockhttp.Created().Multipart(mockhttp.MultipartMixed(
			mockhttp.NewPart().ContentType("text/plain").BodyText("saved"),
		).Boundary("b"))))

	// A Multipart writes request bodies too.
	contentType, body, err := mockhttp.MultipartForm(
		mockhttp.NewPart().Name("title").BodyText("Holiday"),
		mockhttp.NewPart().Name("photo").Filename("me.png").Body([]byte("PNG")),
	).Encode(nil, nil)
	if err != nil {
		panic(err)
	}

	res, err := http.Post(m.URL("/upload"), contentType, bytes.NewReader(body))
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()

	reply, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}

	fmt.Println(res.StatusCode, res.Header.Get(mockhttp.HeaderContentType))
	fmt.Printf("%q\n", reply)
	// Output:
	// 201 multipart/mixed; boundary=b
	// "--b\r\nContent-Type: text/plain\r\n\r\nsaved\r\n--b--\r\n"
}

func Example_eventStream() {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	defer m.Close()

	// Stream a completion as Server-Sent Events, a JSON chunk at a time, as LLM APIs do.
	m.MustMock(mockhttp.Postf("/v1/completions").
		Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(
			mockhttp.NewEvent().DataJSON(map[string]string{"text": "Hel"}),
			mockhttp.NewEvent().DataJSON(map[string]string{"text": "lo"}),
			mockhttp.NewEvent().Name("done").Data("[DONE]"),
		).EventDelay(vlatte.Latency(time.Millisecond)))))

	res, err := http.Post(m.URL("/v1/completions"), mockhttp.MIMEApplicationJSON, strings.NewReader(`{"prompt":"hi"}`))
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()

	stream, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}

	fmt.Println(res.Header.Get(mockhttp.HeaderContentType))
	fmt.Print(string(stream))
	// Output:
	// text/event-stream
	// data: {"text":"Hel"}
	//
	// data: {"text":"lo"}
	//
	// event: done
	// data: [DONE]
}

func ExampleServer_StartRecording() {
	// A target answers, and the server records what it proxies to it.
	target := mockhttp.MustNew(mockhttp.Setup().Silent())
	target.MustStart()
	defer target.Close()
	target.MustMock(mockhttp.Getf("/items").Reply(mockhttp.OK().JSON(map[string]any{"items": []int{1, 2}})))

	dir, err := os.MkdirTemp("", "recordings")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	defer m.Close()
	m.MustMock(mockhttp.AnyMethod().Reply(mockhttp.From(target.URL())))

	if err := m.StartRecording(mockhttp.RecordDir(dir), mockhttp.RecordTransform(func(r *mockhttp.RecordedMock) error {
		r.Name = "the items"
		return nil
	})); err != nil {
		panic(err)
	}

	res, err := http.Get(m.URL("/items?page=2"))
	if err != nil {
		panic(err)
	}
	res.Body.Close()

	// The recordings are written once StopRecording returns.
	recorded, err := m.StopRecording(context.Background())
	if err != nil {
		panic(err)
	}

	for _, r := range recorded.Recordings {
		fmt.Println(r.Name, filepath.Base(r.File))
	}

	// A server that loads the recordings answers without the target.
	target.Close()

	replay := mockhttp.MustNew(mockhttp.Setup().Silent().MockFilePatterns(filepath.Join(dir, "*.mock.json")))
	replay.MustStart()
	defer replay.Close()

	res, err = http.Get(replay.URL("/items?page=2"))
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}

	fmt.Println(res.StatusCode, string(body))
	// Output:
	// the items GET-items-page-2-a59dfd52f96e8038.mock.json
	// 200 {"items":[1,2]}
}

// apiNoMatch answers the requests no mock matched under /api/ in the error shape of the API it stands in for, and
// hands the others to the default renderer.
type apiNoMatch struct {
	fallback mockhttp.NoMatchRenderer
}

func (a apiNoMatch) RenderNoMatch(w http.ResponseWriter, nm *mockhttp.NoMatch) {
	if !strings.HasPrefix(nm.Request.URL.Path, "/api/") {
		a.fallback.RenderNoMatch(w, nm)
		return
	}

	w.Header().Set(mockhttp.HeaderContentType, mockhttp.MIMEApplicationJSON)
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(w, `{"code":"NOT_FOUND","closest":%d}`, len(nm.NearMisses))
}

func ExampleConfigBuilder_NoMatchRenderer() {
	m := mockhttp.MustNew(mockhttp.Setup().Silent().
		NoMatchRenderer(apiNoMatch{fallback: mockhttp.DefaultNoMatchRenderer(mockhttp.NoMatchAuto)}))
	m.MustStart()
	defer m.Close()

	m.MustMock(mockhttp.Getf("/api/users").Header("X-Tenant", match.Equal("a")).Reply(mockhttp.OK()))

	for _, path := range []string{"/api/users", "/health"} {
		res, err := http.Get(m.URL(path))
		if err != nil {
			panic(err)
		}

		body, err := io.ReadAll(res.Body)
		res.Body.Close()

		if err != nil {
			panic(err)
		}

		firstLine, _, _ := strings.Cut(string(body), "\n")
		fmt.Println(res.StatusCode, res.Header.Get(mockhttp.HeaderVlatteError), firstLine)
	}
	// Output:
	// 404 no-match {"code":"NOT_FOUND","closest":1}
	// 418 no-match # Request did not match
}
