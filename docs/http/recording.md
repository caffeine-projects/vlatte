# Recording and playback

A server records what it proxies as mock files, then answers the same requests the same way, without the service.

```go
m := mockhttp.MustNew()
m.MustStart()
t.Cleanup(m.Close)

err := m.StartRecording(
	mockhttp.RecordDir("testdata/recorded"),
	mockhttp.RecordTarget("https://api.example.com"),
	mockhttp.RecordPaths("/v1/*"),
)
// ... requests to m.URL("/v1/...") reach the target, and are recorded ...

res, err := m.StopRecording(ctx) // writes the mock files, then serves them
```

## What is recorded

- With a target, the requests the recording takes go to the target before any mock, and its answers are recorded. The others go to the mocks, as usual.
- Without one, the recording takes what the server proxies: the forward proxy (`Setup().Proxy()`) and proxy replies (`From`, the `proxy` key). A mock's own answer is recorded only with `RecordIncludeMocked(true)`.
- Only an answer the client got whole is recorded. The failures vlatte answers, such as a target it can't reach, aren't.

## Filters

`RecordMethods`, in any case, and `RecordPaths` choose the requests the recording takes. A path is a glob whose `*` matches `/` too, so `/v1/*` takes `/v1/a/b`. `RecordFilter` takes any request pattern, made as a mock is:

```go
mockhttp.RecordFilter(mockhttp.Request().Header("X-Record", match.Equal("yes")))
```

A filter that fails takes nothing: the request is served as usual, and the failure is logged.

## Playback

- `StopRecording` waits until every call it took is written, then serves the recordings: the mock files load again, with the recording's directory, so the hits of file mocks count from 0. When `ctx` ends first, it returns ctx's error, and the recording keeps writing: another `StopRecording` waits for it again, and a `StartRecording` once it wrote everything ends it. Either serves it. A `StopRecording` that `Close` cuts short says the recordings aren't served.
- A server started later serves them too. The default mock file patterns include `testdata/vlatte/_mocks_recorded`, and a server serves the recordings of its `record.dir`.
- A server serves the recordings of the last recording `StopRecording` ended, not those of earlier ones in other directories. The same request recorded into two directories a server serves has one ID twice: `StopRecording` reports it, and the server serves what it served before.
- A mock that proxies every request needs a priority number above 0 to let the recordings answer first: `AnyMethod().Priority(10).Reply(From(target))`.
- A recording answers its request before any proxy, so a later recording without a target doesn't record that request again: it records what is new. A recording with a target records every request it takes.

## What a recording matches

A recording answers the request it recorded, and no other:

| Part | Matched |
|---|---|
| Method | Ignoring case. |
| Path and query | As `url` matches: `url: /items?page=2`. |
| Headers | Those `RecordRequestHeaders` names, by their first value, or their absence. A redacted one by its presence. No other header. |
| Body | A JSON body as JSON, whatever its spacing and member order. A urlencoded or multipart form by all its text fields. Other text as it is, and no body as none. The media type of the `Content-Type` too. |
| Files of a form | By name, content type and content, as `form_file` patterns. |

Calls a recording can't tell apart are one mock, whose answers play in turn, then the last one again. The same answer every time is one. `RecordRepeats(vlatte.RecordFirst)` keeps the first answer only.

A recording doesn't match what it can't read, and then takes any body: one that isn't UTF-8 text, or is larger than `MaxInlineBody`, or that `MaxBodyParsingLimit` cut, or that a parser of the server's config read, or the urlencoded form of a request other than POST, PUT and PATCH, which Go doesn't read. The forward proxy's host isn't recorded: one path on two hosts is one mock.

## What it answers

The status, the headers but those the server sets (`Date`, `Content-Length` and the hop-by-hop ones) and `Set-Cookie`, the trailers, and the body as the client got it, after the steps of the mock.
- A text body up to `MaxInlineBody`, 1 MiB by default, is written in the mock file. Other bodies go to files next to it, named after their content, so equal bodies share a file. `RecordBodyFiles(true)` writes every body to a file.
- A gzip body that fits in `MaxInlineBody` decoded is recorded decoded, with `encoding: gzip`, and compressed again when it is served, into bytes that may differ. So a gzip body with a header or trailer that holds for its bytes, a strong `ETag`, `Content-MD5`, `Digest`, `Content-Digest`, `Repr-Digest` or `Content-Range`, is kept as sent, as are other encoded bodies, with their `Content-Encoding`.
- A larger body goes to a file as it streams, so it never sits in memory whole. When the disk is slower than the sender, the answer waits for it, a second in all at most. Past that, or past `MaxBodySize`, 1 GiB by default, the call isn't recorded, and the client still gets every byte.
- A body file plays back streamed, without a `Content-Length`.
- `RecordExcludeResponseHeaders` sets the headers left out, in place of `Set-Cookie`: name it among them to keep leaving it out, or name none to keep every header. `RecordTransform` changes each recording, as Go values, before it is written:

```go
mockhttp.RecordTransform(func(r *mockhttp.RecordedMock) error {
	for i := range r.Responses {
		delete(r.Responses[i].Header, "X-Request-Id")
	}
	return nil
})
```

## Secrets

Mock files are plain text, and often committed: review them first. They hold the request bodies recordings match, such as a password or a `client_secret`, the values of the headers `RecordRequestHeaders` names, the response headers and bodies, such as tokens, and the query, in the URL and in the file name. `Set-Cookie` is left out by default. The headers `Setup().RedactHeader(...)` names keep their values out too: a response header is written `<redacted>`, and a request header matches by its presence. `RecordTransform` hides the rest.

## Files

A recording writes a mock file named after its request, such as `GET-items-page-2-<hash>.mock.json`, or `.yaml` or `.yml` with `RecordFormat`, into its directory: `{workdir}/_mocks_recorded` by default, `testdata/vlatte/_mocks_recorded`. A mock file appears whole, or not at all.

## Status

`RecordingStatus` counts the calls taken, the mocks written, and the calls dropped or that failed. A response never waits for the calls before it to be written: when too many wait, the next ones are dropped and counted.

## Settings

`Setup().Record(...)` records from start to close, and so does the `record` section of the config:

```yaml
# .vlatterc.yaml
record:
  target: https://api.example.com
  paths: ["/v1/*"]
  request_headers: [Accept]
  exclude_response_headers: [Set-Cookie, X-Request-Id]
```

The target's TLS certificate is verified: a recording sends the target its requests, credentials included. `record.skip_ssl_verify`, or `RecordSkipSSLVerify(true)`, skips it, such as for a self-signed certificate. In Go, `RecordTransport(rt)` sends through a transport of yours instead, such as one that trusts the target's CA, and `skip_ssl_verify` doesn't apply to it. `vlatte --help` lists every `record` setting.

## Admin API

The [admin API](/run/admin-api) starts and stops recordings while the server runs: `POST /recording` takes the keys of the `record` section, `GET /recording` counts the calls, and `DELETE /recording` stops it and lists the files it wrote. Its `/recorder` page does the same from a browser.
