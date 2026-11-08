# Troubleshooting

Find out why a mock is missing or disabled, what a warning asks of the spec, and what to expect from a large spec.

## Warnings

The command logs a warning, at the `WRN` level, for each thing the spec asks that it can't honor, then writes every mock it can. Each warning names the mock, or the place in the spec, it is about.

| Warning | What happened | What to do |
|---|---|---|
| `the example does not fit its schema: converted ...` | An example has the wrong type, but its text reads as the type the schema asks, such as `12345` for a string. The mock sends the converted value. | Fix the example in the spec. |
| `the example breaks its schema's type ...: left out` | An example can't be read as its schema's type, such as `"abc"` for an integer. The mock sends a made-up value instead. | Fix the example in the spec. |
| `the body is data, not text: written disabled: give a string example` | A media type other than JSON has an object or list for its body, which has no text form. | Give that media type a string example, or a `serializedValue` in OpenAPI 3.2. |
| `no body fits: ...: written disabled` | No value fits the schema, as when its `pattern` uses syntax vlatte can't read, such as a lookahead `(?=`. | Give the response an example. |

## Mocks written disabled

A mock written disabled matches no request until you [turn it on](/openapi/replies#which-mocks-are-on). The command writes a mock disabled when:

- Its response is one a request can't predict, such as a `503`, a second success status or a second named example. This is on purpose: turn it on to test how a client copes.
- Its body can't be built, as a [warning](#warnings) says.

Media types such as `application/vnd.acme.thing`, without a `+json` suffix, are not read as JSON, so their bodies need a string example too. Name them `application/vnd.acme.thing+json` in the spec to have them treated as JSON.

## A request gets an unexpected answer

Every answer carries the `Vlatte-Matched-Mock-Id` header, which names the mock that answered, and `Vlatte-Matched-Mock-Source`, the file it came from:

```sh
curl -i http://127.0.0.1:8080/books/abc
# HTTP/1.1 400 Bad Request
# Vlatte-Matched-Mock-Id: books-_isbn-get-400
# Vlatte-Matched-Mock-Source: testdata/vlatte/_mocks/books-_isbn-get-400.mock.yaml
```

An unexpected `400`, `401`, `415` or `422` means the request breaks the spec; when the spec declares no response of that status, the answer lists what it breaks. See [Requests that break the spec](/openapi/contract). When no mock answers, the server's [near misses](/http/troubleshooting#near-misses) say which came closest.

## Large specs

The command takes seconds on most specs, and longer on specs with thousands of operations and schemas. The `info` log says what it is doing. It writes the files only once every mock is ready, so Ctrl+C stops it without leaving a half-written folder.
