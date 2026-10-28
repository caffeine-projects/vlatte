# Record and replay

This guide records a real API once, commits what it answered as mock files, and replays them in every later test run, without the API. It uses [recording and playback](/http/recording) and [mock files](/http/mock-files).

## A server that records on demand

The helper below starts a server that serves the recordings in `testdata/api`. Run the tests with `-record` and it sends the requests to the real API instead, and records its answers.

```go
var record = flag.Bool("record", false, "record the real API into testdata/api")

const target = "https://api.example.com"

func newAPI(t *testing.T) *mockhttp.Server {
	t.Helper()

	m := mockhttp.MustNew(mockhttp.Setup().
		MockFilePatterns("testdata/api/*.mock.json").
		RedactHeader("Authorization"))
	m.MustStart()
	t.Cleanup(m.Close)

	if *record {
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

	return m
}
```

```sh
go test ./... -run TestUsers -args -record   # once, against the real API
go test ./...                                # every other run, from the recordings
```

A test uses the server as it would the API:

```go
func TestUsers(t *testing.T) {
	m := newAPI(t)
	client := &Client{BaseURL: m.URL()}
	// ...
}
```

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

A recording matches its request's method, path and query, and its body, and no other request. `RecordRequestHeaders` adds headers it matches, and calls it can't tell apart play their answers in turn. See [What a recording matches](/http/recording#what-a-recording-matches).

## Keep secrets out

Recordings are plain text, and they get committed: review them first. The helper keeps the `Authorization` header's value out with `RedactHeader`, and drops a header that changes on every call with `RecordTransform`. `Set-Cookie` is left out by default. See [Secrets](/http/recording#secrets).

## Record with the CLI

The `vlatte` command records the same way, from start to stop:

```sh
vlatte --record --record-target=https://api.example.com --record-paths='/v1/*'
# Ctrl-C writes the recordings into testdata/vlatte/_mocks_recorded
vlatte   # serves them
```

The admin API's `/recorder` page starts and stops a recording from a browser.

## What you used

- `StartRecording`, `StopRecording`, `RecordDir`, `RecordTarget`, `RecordTransform`: [Recording and playback](/http/recording).
- `Setup().MockFilePatterns`, `Setup().RedactHeader`: [Mock files](/http/mock-files), [Troubleshooting](/http/troubleshooting#logs).
