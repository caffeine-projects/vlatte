# Mock files and the CLI

This guide writes mock files with editor support, serves them with the `vlatte` command, changes them while it runs, and loads the same files in Go tests. It uses [mock files](/http/mock-files), [path templates](/http/matching#path-templates), [templates](/templating/), the [CLI](/run/cli) and the [admin API](/admin-api/).

## Write the files

The command serves the files matching `testdata/vlatte/_mocks/*mock.yaml` and `*mock.yml`. The first line of each names its [schema](/reference/schemas), so editors complete keys and flag mistakes as you type.

```yaml
# yaml-language-server: $schema=https://vlatte.dev/schemas/http.schema.json
# testdata/vlatte/_mocks/get-user_mock.yaml
id: get-user
name: get a user
request:
  method: GET
  path_template: /users/{id}
  path_params:
    id: [regex, '^[0-9]+$']
response:
  status: 200
  header:
    content-type: application/json
  body: '{"id": {{ .Request.PathParams.id }}, "name": "user {{ .Request.PathParams.id }}"}'
  template:
    enabled: true
```

```yaml
# yaml-language-server: $schema=https://vlatte.dev/schemas/http.schema.json
# testdata/vlatte/_mocks/create-user_mock.yaml
id: create-user
request:
  method: POST
  path: /users
  header:
    content-type: application/json
  body: [jsonpath, $.name, [present]]
response:
  status: 201
  header_template:
    location: '/users/{{ randomInt 100 999 }}'
  body: '{"name": {{ (fromJSON .Request.Body.String).name | toJSON }}}'
  template:
    enabled: true
```

`path_template` matches `/users/7` and reads `7` as `.Request.PathParams.id`. The second file answers with the name the request sent, read with `fromJSON`, and `toJSON` writes it back as a JSON string.

## Serve them

```sh
vlatte --addr=127.0.0.1:8080 --admin-addr=127.0.0.1:8081
```

```sh
curl localhost:8080/users/7
# {"id": 7, "name": "user 7"}

curl -i -X POST -H 'Content-Type: application/json' -d '{"name": "ann"}' localhost:8080/users
# HTTP/1.1 201 Created
# Location: /users/124
# {"name": "ann"}
```

A request no mock matches gets `418`, and a body that says which mocks came closest and why they didn't match. It is Markdown, or JSON for a client that asks for it: see [near misses](/http/troubleshooting#near-misses).

```md
# Request did not match

No mock matched `GET http://127.0.0.1:8080/nope`.

Request ID: `0f8c2b1e-6d4a-4c3e-9a57-2b8e1d4f7c90`

## Closest mocks

1. `get-user` (`get a user`): 1 of 3 parts matched; differs in `path_template`, `path_params id`

## Next steps

- Change the request, or the closest mock, in the parts they differ in.
- See the request in the journal: `GET http://127.0.0.1:8081/requests/0f8c2b1e-6d4a-4c3e-9a57-2b8e1d4f7c90`
```

## Change mocks while it runs

The admin API takes mocks in the same format, as JSON or YAML:

```sh
curl -X POST -H 'Content-Type: application/json' \
  -d '{"request": {"method": "GET", "path": "/health-check"}, "response": {"status": 204}}' \
  localhost:8081/mocks
```

The command keeps the mocks it adds across restarts, in `testdata/vlatte/_admin`. `POST /mocks/save` writes them as mock files into `_mocks`, to commit with the others. After editing a file, `POST /mocks/reload` loads the files again, or restart the command.

The admin API also answers what the server got: `curl -X QUERY -H 'Content-Type: application/json' -d '{"method": "POST", "path": "/users", "limit": 0}' localhost:8081/requests` counts the requests to create users.

## Use the same files in Go tests

A server made in Go loads the same folder on start, relative to the test's package directory:

```go
func TestWithMockFiles(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	res, err := http.Get(m.URL("/users/7"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || string(body) != `{"id": 7, "name": "user 7"}` {
		t.Fatalf("status = %d, body = %s", res.StatusCode, body)
	}
}
```

`Setup().MockFilePatterns(...)` loads other files, `m.MustMock(mockhttp.FromFile(path))` loads one, and `m.Reload()` loads them again.

## What you used

- [Mock files](/http/mock-files) and their [schemas](/reference/schemas).
- [Path templates](/http/matching#path-templates) and [templates](/templating/).
- The [CLI](/run/cli) and the [admin API](/admin-api/).
