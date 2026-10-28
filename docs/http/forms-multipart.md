# Forms and multipart

Go's `net/http` parses request bodies: `ParseForm` reads urlencoded forms and `ParseMultipartForm` reads `multipart/form-data`. Bodies are parsed by their media type, in any case: `text/plain` and `application/json` as text, and the two form types as forms.

## Fields

`FormField` (file key `form`) reads a field of either encoding, as `http.Request.PostFormValue` does: its first value, and never the query. `FormFields` (file key `forms`) reads every value.

## Files

`FormFile` (file key `form_file`) matches the files of a field with a pattern. Some file of the field must meet every condition of the pattern, or every file with `Every()` (`every: true`). With several patterns for one field, each must be met by some file of the field, and one file may meet several.

```go
m.MustMock(mockhttp.Postf("/upload").
	FormField("title", match.Equal("Holiday")).
	FormFile("avatar", mockhttp.FileWith().
		Filename(match.HasSuffix(".png")).
		ContentType(match.Equal("image/png"))).
	Reply(mockhttp.Created()))
```

```yaml
request:
  method: POST
  path: /upload
  form:
    title: Holiday
  form_file:
    avatar:
      filename: [hasSuffix, .png]
      content_type: image/png   # the media type, in lower case, without parameters
      body_file: avatar.png     # the file's bytes; or body: text or a matcher list
```

Go's rules apply:
- a part without a field name isn't read;
- a file's name is its base: `dir/a.png` arrives as `a.png`;
- a text field keeps no headers;
- a form holds at most 1000 parts, which `GODEBUG=multipartmaxparts` changes.

A form body that doesn't parse, or that was cut short, has no fields at all, and the server logs a warning. Other multipart requests, such as `multipart/mixed`, aren't parsed: match them with `Body`.

## Multipart replies

`Reply.Multipart` writes a body of any multipart subtype with Go's `multipart.Writer`. The body sets the `Content-Type`, with its boundary. When none is set, a random boundary is drawn once per mock, so every response is the same bytes.

```go
m.MustMock(mockhttp.Getf("/report").Reply(mockhttp.OK().Multipart(mockhttp.NewMultipart("related",
	mockhttp.NewPart().Header("Content-Id", "<root>").BodyJSON(map[string]any{"id": 1}),
	mockhttp.NewPart().Name("report").Filename("report.pdf").BodyFile("testdata/report.pdf"),
).Param("start", "<root>"))))
```

```yaml
response:
  status: 200
  multipart:
    type: related              # the subtype: form-data, mixed, related, x-mixed-replace…
    params: {start: "<root>"}
    parts:
      - header: {content-id: "<root>"}
        body: {id: 1}          # text as is, anything else as JSON
      - name: report
        filename: report.pdf
        body_file: report.pdf  # relative to the mock file
```

- **Bodies.** A part takes one of `body`, `body_base64` and `body_file` at most. A body that isn't UTF-8 text, such as an image, goes in `body_base64`, as standard base64 (`body_base64: //4A`), whose line breaks are ignored, or in a `body_file`. A byte body set in Go with `Part.Body` describes in `body_base64` when it isn't UTF-8, or when the body is templated, so it loads back as bytes, not as a template.
- **Defaults.** A part with a name or a filename gets its `Content-Disposition`. A JSON body defaults to `application/json`, and a part with a filename to `application/octet-stream`.
- **Templates.** With `template.enabled`, or `Template()` in Go, the text of the parts is a template: names, filenames, content types, header values, text bodies and body file names. Byte bodies, such as `body_base64`, JSON bodies and the content of body files are written as they are.
- **Encode.** `Multipart.Encode` writes the same bodies for requests in tests.

## Streaming

A part with a `delay` waits that long before it is written, and everything before it is flushed first. A client therefore reads each part as it comes, as `multipart/x-mixed-replace` camera feeds need.

```yaml
response:
  status: 200
  multipart:
    type: x-mixed-replace
    parts:
      - {content_type: image/jpeg, body_file: frame1.jpg}
      - {content_type: image/jpeg, body_file: frame2.jpg, delay: 500ms}
```

**Webhooks** send multipart bodies too:
- the `multipart` parameter takes the same object, and `multipart_template` makes its text a template;
- Go uses `webhook.Options().Multipart(mockhttp.MultipartForm(...))`;
- a webhook sends its body at once, so its parts can't have delays;
- relative body files open from the mock file's directory, as in replies: a whole `body_file` and a part's alike.

**Templates** read forms: `{{ .Request.PostForm.Get "title" }}` and `{{ with .Request.FormFile "avatar" }}{{ .Filename }}{{ end }}`.

**Recording** matches a recorded upload by all its text fields, each with its values in order, and its files by `form_file` patterns, so it replays whatever boundary the next client picks. Files that aren't UTF-8 text go to files next to the recording. An upload with another field doesn't match; one with more files still does. See [Recording and playback](/http/recording).
