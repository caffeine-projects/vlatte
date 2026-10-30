# Files and secrets

Find where recordings are written and how to keep credentials and sensitive payloads out of saved fixtures.

## Files

A recording writes a mock file named after its request, such as `GET-items-page-2-<hash>.mock.json`, or `.yaml` or `.yml` with `RecordFormat`, into its directory: `{workdir}/_mocks_recorded` by default, `testdata/vlatte/_mocks_recorded`. A mock file appears whole, or not at all.

## Secrets

Mock files are plain text, and often committed: review them first. They hold the request bodies recordings match, such as a password or a `client_secret`, the values of the headers `RecordRequestHeaders` names, the response headers and bodies, such as tokens, and the query, in the URL and in the file name. `Set-Cookie` is left out by default. The headers `Setup().RedactHeader(...)` names keep their values out too: a response header is written `<redacted>`, and a request header matches by its presence. `RecordTransform` hides the rest.
