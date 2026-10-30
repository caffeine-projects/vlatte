# Recording through the admin API

The [admin API](/admin-api/) starts and stops recordings while the server runs. Its `/recorder` page provides the same controls in a browser.

## Start a recording

`POST /recording` takes the keys of the `record` settings section. Send the app's requests to the mock server after starting it:

```sh
curl -X POST -H 'Content-Type: application/json' \
  -d '{"target":"https://api.example.com","paths":["/v1/*"]}' \
  http://127.0.0.1:8081/recording
```

Use the HTTP server's admin address for HTTP recording, and the gRPC server's admin address for gRPC recording. The CLI prints those addresses when it starts.

## Check progress

`GET /recording` reports recording status, including how many calls were taken, written, dropped or failed:

```sh
curl http://127.0.0.1:8081/recording
```

## Stop and replay

`DELETE /recording` stops the recording and lists the files it wrote:

```sh
curl -X DELETE http://127.0.0.1:8081/recording
```

The server then serves those files. Review them for [secrets](/recording/files-secrets#secrets) before committing them. The [OpenAPI reference](/reference/admin-api.html){target="_blank" rel="noopener noreferrer"} describes the request and response formats.
