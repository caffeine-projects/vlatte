<p align="center">
  <img src="docs/brand/logo.svg" alt="" width="128" height="128">
</p>

<h1 align="center">VLatte</h1>

<p align="center">
  Mock HTTP and gRPC servers for Go tests,<br>
  and a CLI that serves them from files.
</p>

<p align="center">
  <a href="https://vlatte.dev/"><strong>vlatte.dev</strong></a>
  <br>
</p>

<p align="center">
  <a href="https://github.com/caffeine-projects/vlatte/releases"><img src="https://img.shields.io/github/v/release/caffeine-projects/vlatte?include_prereleases&sort=semver" alt="Release"></a>
  <a href="https://pkg.go.dev/github.com/caffeine-projects/vlatte"><img src="https://pkg.go.dev/badge/github.com/caffeine-projects/vlatte.svg" alt="Go Reference"></a>
  <br>
  <br>
</p>

<p align="center">
  <a href="https://github.com/caffeine-projects/vlatte/actions/workflows/ci.yml"><img src="https://github.com/caffeine-projects/vlatte/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://vlatte.dev/"><img src="https://github.com/caffeine-projects/vlatte/actions/workflows/docs.yml/badge.svg" alt="Docs"></a>
  <a href="https://codecov.io/gh/caffeine-projects/vlatte"><img src="https://codecov.io/gh/caffeine-projects/vlatte/graph/badge.svg" alt="codecov"></a>
  <a href="https://sonarcloud.io/summary/new_code?id=caffeine-projects_vlatte"><img src="https://sonarcloud.io/api/project_badges/measure?project=caffeine-projects_vlatte&metric=alert_status" alt="Quality gate status"></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/caffeine-projects/vlatte"><img src="https://api.scorecard.dev/projects/github.com/caffeine-projects/vlatte/badge" alt="OpenSSF Scorecard"></a>
  <a href="https://www.bestpractices.dev/projects/16055"><img src="https://www.bestpractices.dev/projects/16055/badge" alt="OpenSSF Best Practices"></a>
</p>

## About

VLatte starts real HTTP and gRPC servers that answer the way you tell them to. Start one in a Go test with `mockhttp` or `mockgrpc`, or run the `vlatte` CLI to serve mocks from YAML or JSON files. Either way, mocks match requests on any part of them and reply with static or templated answers.

**[Read the documentation](https://vlatte.dev/)**

## Features

- **[HTTP](https://vlatte.dev/http/) and [gRPC](https://vlatte.dev/grpc/) mock servers.** gRPC needs no service implementation: unary, server, client and bidi streaming calls, from generated code or descriptor sets.
- **[Matchers](https://vlatte.dev/matchers/)** shared by every protocol: equality, JSON path, JSON Schema, XPath, regex, globs, dates and combinators, plus your own.
- **Replies** with templated headers and bodies, sequences, random picks, echo, multipart, Server-Sent Events and custom functions.
- **Mock files** in YAML or JSON, with [JSON schemas](https://vlatte.dev/reference/schemas) for editors, reloaded on demand.
- **Scenarios** for stateful flows, and **webhooks** sent after a mock answers.
- **Proxying, and recording** of a real service to play it back later, with outbound rules that limit the addresses a server connects to.
- **Fault injection:** latency, slow bandwidth, connection resets, malformed and empty answers.
- **[Request verification](https://vlatte.dev/verify/)** of what clients sent: how many requests, by when and in what order, from Go tests, the admin API or MCP, with the closest requests and the parts they missed when a check fails.
- **A [request journal](https://vlatte.dev/more/journal)** of every request a server gets, and debug headers on every answer.
- **An [admin API](https://vlatte.dev/reference/admin-api.html)** to manage mocks while a server runs, with Basic or bearer authentication and TLS, also served over **MCP** for AI agents.
- **[A CLI and a Docker image](https://vlatte.dev/run/)** to run mock servers outside Go.

## Install

The library, for Go 1.27 or later:

```sh
go get github.com/caffeine-projects/vlatte
```

The CLI, with Homebrew, an archive from [GitHub Releases](https://github.com/caffeine-projects/vlatte/releases), Go, or Docker:

### Homebrew

```sh
brew install caffeine-projects/tap/vlatte
```

### Go

```sh
go install github.com/caffeine-projects/vlatte/cmd/vlatte@latest
```

### Docker

```sh
docker run --rm ghcr.io/caffeine-projects/vlatte:v0.1.0-rc.1 version
```

Release archives and images are signed: see [Verifying a release](https://vlatte.dev/getting-started/installation#verifying-a-release).


## Quick start

### HTTP in a test

```go
func TestCreateUser(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	created := m.MustMock(mockhttp.Postf("/users").
		Header("Authorization", match.HasPrefix("Bearer ")).
		Body(match.JSONPath("$.name", match.Equal("ann"))).
		Reply(mockhttp.Created().
			HeaderTemplate("Location", `/users/{{ uuid }}`).
			ContentType("application/json").
			BodyTemplate(`{"name": {{ (fromJSON .Request.Body.String).name | toJSON }}}`)))

	// Point your client at m.URL() and call it.
	user, err := users.NewClient(m.URL(), "token").Create(t.Context(), "ann")
	// 201, Location: /users/<uuid>, body {"name": "ann"}

	created.AssertNumberOfCalls(t, 1)
}
```

### gRPC in a test

```go
func TestGreeter(t *testing.T) {
	m := mockgrpc.MustNew(mockgrpc.Setup().InMemory()) // no port: only m.Dial reaches it
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockgrpc.Method("Greeter/SayHello").
		Field("name", match.HasPrefix("a")).
		Reply(mockgrpc.Template(`{"message": "hi {{ .Request.Message.name }}"}`)))

	m.MustMock(mockgrpc.Method("Greeter/Watch").
		Reply(mockgrpc.Messages(&pb.Event{Id: "1"}, &pb.Event{Id: "2"}).
			MessageDelay(100 * time.Millisecond).
			Status(codes.Unavailable, "restarting")))

	client := pb.NewGreeterClient(m.MustDial()) // closed with the server
	res, err := client.SayHello(t.Context(), &pb.HelloRequest{Name: "ann"})
	// res.Message == "hi ann"; Watch sends 1, 2, then Unavailable
}
```

### CLI with a mock file

```yaml
# testdata/vlatte/_mocks/users_mock.yaml
request:
  method: GET
  path_template: /users/{id}
  path_params:
    id: [regex, '^[0-9]+$']
  header:
    authorization: [hasprefix, "Bearer "]
response:
  status: 200
  header:
    content-type: application/json
  body: '{"id": {{ .Request.PathParams.id }}, "name": "ann"}'
  template:
    enabled: true
delay: 150ms
```

```sh
vlatte --addr=127.0.0.1:8080
curl -H 'Authorization: Bearer x' http://127.0.0.1:8080/users/7
# {"id": 7, "name": "ann"}
```

## Documentation

The [documentation](https://vlatte.dev/) covers each protocol, the shared features, the CLI and Docker, and the mock file and admin API references. The Go API is on [pkg.go.dev](https://pkg.go.dev/github.com/caffeine-projects/vlatte).

## Development

```sh
make test   # run the tests with the race detector
make lint   # run golangci-lint
```

## License

[MIT](LICENSE)
