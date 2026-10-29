---
layout: home
markdownStyles: false
title: vlatte
titleTemplate: API mocking for Go
description: Mock HTTP and gRPC servers for Go tests, and a CLI that serves them from files.
---

<script setup>
import HomeCards from './.vitepress/theme/components/home/HomeCards.vue'
import HomeCta from './.vitepress/theme/components/home/HomeCta.vue'
import HomeHero from './.vitepress/theme/components/home/HomeHero.vue'
import HomeRun from './.vitepress/theme/components/home/HomeRun.vue'
import HomeSection from './.vitepress/theme/components/home/HomeSection.vue'
import HomeSplit from './.vitepress/theme/components/home/HomeSplit.vue'
import HomeStats from './.vitepress/theme/components/home/HomeStats.vue'

const strengths = [
  { title: 'HTTP and gRPC', badge: 'unary to bidi', text: 'Real servers in your tests. gRPC needs no service implementation: any method, any call type.', link: '/getting-started/concepts' },
  { title: 'Matchers', badge: 'JSONPath · XPath · Schema', text: 'Match the method, path, headers, query, forms, files and body, with the same matchers on both protocols.', link: '/matchers/' },
  { title: 'Templated replies', badge: 'Go templates', text: 'Build headers and bodies from the request, with functions for JSON, strings, time and hashes.', link: '/templating/' },
  { title: 'Record and replay', badge: 'no API needed', text: 'Proxy a real service once, save what passes through as mock files, and play it back without it.', link: '/recording/' },
  { title: 'Faults', badge: 'resets · latency', text: 'See how clients cope with slow networks, dribbled bodies, resets, garbage and timeouts.', link: '/http/faults' },
  { title: 'Scenarios', badge: 'stateful flows', text: 'Mocks that answer by state, for flows that change as they go: a cart that fills, a job that ends.', link: '/scenarios/' },
  { title: 'TLS in one line', badge: 'mTLS too', text: 'HTTPS and gRPC over TLS with no certificate to make, or with yours, and client certificates if you like.', link: '/tls/' },
  { title: 'Outside Go', badge: 'CLI · Docker · MCP', text: 'Serve mock files with the vlatte command or Docker, and change mocks while they run, over REST or MCP.', link: '/run/' },
]

const toolbox = [
  { title: 'Matchers', text: 'Every matcher, in Go and in mock files, and how to write your own.', link: '/matchers/' },
  { title: 'Recording', text: 'Record HTTP and gRPC services as mock files, from Go, the CLI or the admin API.', link: '/recording/' },
  { title: 'Recipes', text: 'Ready-made setups, such as a server that proxies every request.', link: '/recipes/' },
  { title: 'Webhooks', text: 'Call other services after a mock answers, with templated requests.', link: '/webhooks/' },
  { title: 'Request journal', text: 'What clients sent, to verify in tests or read over the admin API.', link: '/more/journal' },
  { title: 'Mock file schemas', text: 'JSON schemas that give your editor completion and checks for mock files.', link: '/reference/schemas' },
]
</script>

<div class="vl-home">

<HomeHero>

::: code-group

```go [Go test]
func TestCharge(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	paid := m.MustMock(mockhttp.Postf("/payments").
		Header("Authorization", match.HasPrefix("Bearer ")).
		Body(match.JSONPath("$.amount", match.GreaterThan(0))).
		Reply(mockhttp.Created().
			JSON(map[string]any{"id": "pay_1"})))

	client := payments.NewClient(m.URL(), "token")
	_, err := client.Charge(t.Context(), 42)

	require.NoError(t, err)
	paid.AssertCalled(t)
}
```

```go [gRPC]
func TestGreeter(t *testing.T) {
	m := mockgrpc.MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockgrpc.Method("greeter.Greeter/SayHello").
		Field("name", match.HasPrefix("a")).
		Reply(mockgrpc.Message(&pb.HelloReply{Message: "hi ann"})))

	client := pb.NewGreeterClient(m.MustDial())
	res, err := client.SayHello(t.Context(), &pb.HelloRequest{Name: "ann"})

	require.NoError(t, err)
	require.Equal(t, "hi ann", res.GetMessage())
}
```

```yaml [Mock file]
# testdata/vlatte/_mocks/payments_mock.yaml
request:
  method: POST
  path: /payments
  header:
    authorization: [hasprefix, "Bearer "]
  body: [jsonpath, $.amount, [gt, 0]]
response:
  status: 201
  header:
    content-type: application/json
  body: {id: pay_1}
```

```sh [CLI]
vlatte --https --addr=:8443

curl -k https://localhost:8443/payments \
  -H 'Authorization: Bearer token' \
  -H 'Content-Type: application/json' \
  -d '{"amount": 42}'
# 201 {"id":"pay_1"}
```

:::

</HomeHero>

<HomeStats />

<HomeSection title="Mocks that behave like the real thing" lead="Everything a test needs from a server it doesn't own: the right answer, the wrong one, and the slow one.">
  <HomeCards :items="strengths" />
</HomeSection>

<HomeSection title="Run it your way" lead="The same mocks, in a test, from files, or in a container.">
<HomeRun>

::: code-group

```go [Go test]
// go get github.com/caffeine-projects/vlatte
m := mockhttp.MustNew()
m.MustStart()
t.Cleanup(m.Close)

m.MustMock(mockhttp.Getf("/health").Reply(mockhttp.OK()))
```

```sh [Mock files]
brew install caffeine-projects/tap/vlatte # or go install github.com/caffeine-projects/vlatte/cmd/vlatte@latest

# Serves the mock files of testdata/vlatte/_mocks.
vlatte --addr=:8080
```

```yaml [Docker Compose]
services:
  payments:
    image: ghcr.io/caffeine-projects/vlatte:v0.1.0-rc.1
    ports: ["8080:8080"]
    volumes:
      - ./mocks:/vlatte/testdata/vlatte/_mocks:ro
```

:::

</HomeRun>
</HomeSection>

<HomeSplit title="HTTPS without the certificate dance" link="/tls/" link-text="TLS">

`MustStartTLS` serves HTTPS, or gRPC over TLS, with a certificate built in: no openssl, no files to keep.

Bring your own certificate when you need one, or ask clients for theirs with mutual TLS. The CLI and the Docker image do the same with a flag.

<template #code>

::: code-group

```go [HTTP]
m := mockhttp.MustNew()
m.MustStartTLS() // m.URL() starts with https://
t.Cleanup(m.Close)
```

```go [gRPC]
m := mockgrpc.MustNew()
m.MustStartTLS()
t.Cleanup(m.Close)

conn := m.MustDial() // trusts the server's certificate
```

```go [mTLS]
m := mockhttp.MustNew(mockhttp.Setup().
	TLSMutual("localhost.pem", "localhost-key.pem", "ca.pem"))
m.MustStartTLS()
```

```sh [CLI]
vlatte --https
```

:::

</template>
</HomeSplit>

<HomeSplit title="Break things on purpose" link="/guides/chaos-testing" link-text="Chaos testing" reverse>

Clients fail in the ways their servers do. Faults make a mock reset the connection, trickle its body, send garbage or never answer, so you see your retries and timeouts work before production does.

<template #code>

::: code-group

```go [Go]
m.MustMock(
	// The first call loses its connection.
	mockhttp.Getf("/report").Once().
		WirePipe(fault.ConnectionResetByPeer()).
		Reply(mockhttp.OK()),
	// The retry gets its body over a 3G link.
	mockhttp.Getf("/report").Priority(1).
		Pipe(fault.Bandwidth(fault.Bandwidth3G)).
		Reply(mockhttp.OK().BodyFile("testdata/report.csv")),
)
```

```yaml [Mock file]
request: {method: GET, path: /report}
response: {status: 200, body_file: report.csv}
pipes:
  - type: bandwidth
    rate: 3g
```

:::

</template>
</HomeSplit>

<HomeSplit title="Mocks your AI agent can drive" link="/mcp/" link-text="MCP">

Over MCP, a coding agent sees every mock, writes new ones, records a real service and checks what your client sent, with the tools the admin API serves.

Start `vlatte mcp` from the agent's settings, or point it at `/mcp` on a server that runs already.

<template #code>

::: code-group

```json [Agent settings]
{"mcpServers": {"vlatte": {"command": "vlatte", "args": ["mcp", "--mock-files=mocks/*.yaml"]}}}
```

```go [Go]
api := admin.MustNew(admin.MCP())
m := mockhttp.MustNew(mockhttp.Setup().Admin(api))
m.MustStart()

fmt.Println(m.AdminURL() + "/mcp") // Streamable HTTP
```

:::

</template>
</HomeSplit>

<HomeSection title="Toolbox" lead="More of what comes in the box.">
  <HomeCards :items="toolbox" style="--vl-cards: 3" />
</HomeSection>

<HomeCta />

</div>
