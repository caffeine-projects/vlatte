# Record and replay

This guide records a real API once, commits what it answered as mock files, and replays them in every later test run, without the API. It uses [recording and playback](/recording/) and [mock files](/http/mock-files).

## A server that records on demand

The test below serves the recordings in `testdata/api`. With `-record`, it sends the request to `API_URL` instead and records the answer. [Install the library](/getting-started/installation#the-library), then save this as `record_test.go` in a directory of its own inside your Go module:

```go
package recording_test

import (
	"context"
	"flag"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/caffeine-projects/vlatte/mockhttp"
)

var _record = flag.Bool("record", false, "record the API into testdata/api")

func TestUsers(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().
		MockFilePatterns("testdata/api/*.mock.json").
		RedactHeader("Authorization"))
	m.MustStart()
	t.Cleanup(m.Close)

	if *_record {
		target := os.Getenv("API_URL")
		if target == "" {
			t.Fatal("API_URL must name the upstream to record")
		}

		err := m.StartRecording(
			mockhttp.RecordDir("testdata/api"),
			mockhttp.RecordTarget(target),
			mockhttp.RecordTransform(func(r *mockhttp.RecordedMock) error {
				for i := range r.Responses {
					delete(r.Responses[i].Header, "X-Request-Id")
				}
				return nil
			}),
		)
		if err != nil {
			t.Fatal(err)
		}

		// Cleanups run last first: the recordings are written before the server closes.
		t.Cleanup(func() {
			if _, err := m.StopRecording(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.URL("/users/1"), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || string(body) != `{"id":1}` {
		t.Fatalf("status = %d, body = %s", res.StatusCode, body)
	}
}
```

The cleanup that stops recording uses `context.Background()` because the test's context is canceled before its cleanups run. It is registered after `m.Close`, so it writes the recordings before the server closes.

## Record, then replay without the upstream

For a local walkthrough, start this small upstream in another terminal. It returns the response the test expects:

```sh
python3 - <<'PY'
from http.server import BaseHTTPRequestHandler, HTTPServer

class API(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path != "/users/1":
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"id":1}')

HTTPServer(("127.0.0.1", 9000), API).serve_forever()
PY
```

Record it once:

```sh
API_URL=http://127.0.0.1:9000 go test . -run '^TestUsers$' -args -record
```

Check that `testdata/api` holds the recording, then stop the upstream with Ctrl-C and run the test again without `-record`:

```sh
go test . -run '^TestUsers$'
```

The same status and body checks now pass using the files alone. For your own API, use its URL and client call, and assert the response your app depends on. Review the recordings before committing them.

## What a recording looks like

Each call becomes a mock file, named after its request:

```json
// testdata/api/GET-users-1-f20a0db5256583fc.mock.json
{
  "id": "GET-users-1-f20a0db5256583fc",
  "name": "GET /users/1",
  "request": {
    "method": "GET",
    "url": "/users/1"
  },
  "response": {
    "body": "{\"id\":1}",
    "header": {
      "Content-Type": [
        "application/json"
      ]
    },
    "status": 200
  }
}
```

A recording matches its request's method, path and query, and its body, and no other request. `RecordRequestHeaders` adds headers it matches, and calls it can't tell apart play their answers in turn. See [What a recording matches](/recording/http#what-a-recording-matches).

## Keep secrets out

Recordings are plain text, and they get committed: review them first. The server's `RedactHeader` keeps the `Authorization` header's value out, and `RecordTransform` drops a header that changes on every call. `Set-Cookie` is left out by default. See [Secrets](/recording/files-secrets#secrets).

## Record with the CLI

The `vlatte` command records the same way, from start to stop:

```sh
vlatte --record --record-target=https://api.example.com --record-paths='/v1/*'
# Ctrl-C writes the recordings into testdata/vlatte/_mocks_recorded
vlatte   # serves them
```

The admin API's `/recorder` page starts and stops a recording from a browser. See [Recording from the CLI](/recording/cli), [Recording settings](/recording/settings), and [Recording through the admin API](/recording/admin-api).

## What you used

- `StartRecording`, `StopRecording`, `RecordDir`, `RecordTarget`, `RecordTransform`: [Recording and playback](/recording/).
- `Setup().MockFilePatterns`, `Setup().RedactHeader`: [Mock files](/http/mock-files), [Troubleshooting](/http/troubleshooting#logs).
