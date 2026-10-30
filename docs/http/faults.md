# Faults

The `fault` package has pipes that make an HTTP mock answer wrong, to test how a client handles a server that fails, or a bad network. A pipe of either level acts on the body with `Pipe`, and on every byte of the answer, its head included, with `WirePipe`:

## Fault pipes

| Pipe | Level | What the client gets |
|---|---|---|
| `fault.EmptyResponse()` | `WirePipe` | No byte at all: the connection ends where it waits for a status line. |
| `fault.MalformedResponseChunk()` | `WirePipe` | The status line and headers, with the mock's status, then garbage in place of the first chunk of the body, then the end of the connection. |
| `fault.RandomDataThenClose()` | `WirePipe` | `fault.RandomDataSize` random bytes in place of the answer, then the end of the connection. |
| `fault.ConnectionResetByPeer()` | `WirePipe` | A TCP reset, such as `ECONNRESET`, before a status line. |
| `fault.ChunkedDribbleDelay(chunks, total)` | `Pipe` | The whole body, in `chunks` flushed parts spread over `total`: part k comes at k·total/chunks, and the body ends at `total`. The body is held until the reply wrote all of it. |
| `fault.Latency(delay)` | Either | Every write and flush after a duration chosen by `delay`, in order. The answer keeps its pace: the writer doesn't wait. |
| `fault.Bandwidth(bytesPerSecond)` | Either | Each write at that rate, in flushed pieces of a tenth of a second. `fault.Bandwidth3G` and the other `fault.Bandwidth*` constants are common links, in bytes per second. |
| `fault.Slicer(averageSize, sizeVariation, delay)` | Either | Each write in flushed slices of `averageSize` bytes, give or take `sizeVariation` at random, `delay` apart. |
| `fault.SlowClose(delay)` | Either | The whole answer, then its end `delay` later: the body's last chunk with `Pipe`, the close of the connection with `WirePipe`. |
| `fault.Timeout(timeout)` | Either | Nothing, then the end of the connection `timeout` after the answer began; with `Pipe`, the head comes with that end. A timeout of 0 never ends. |
| `fault.LimitData(n)` | Either | The first `n` bytes, then the end of the connection: bytes of the body with `Pipe`, of the whole answer with `WirePipe`. |
| `fault.ConnectionResetByPeerAfter(d)` | Either | The answer as it comes, then a TCP reset `d` after it began. With `Pipe` over HTTP/2, the stream is cut instead. |

```go
m.MustMock(mockhttp.Getf("/users").WirePipe(fault.ConnectionResetByPeer()).Reply(mockhttp.OK()))
m.MustMock(mockhttp.Getf("/report").Pipe(fault.ChunkedDribbleDelay(5, 2*time.Second)).Reply(mockhttp.OK().BodyFile("report.csv")))
m.MustMock(mockhttp.Getf("/search").WirePipe(fault.Latency(vlatte.Jitter(300*time.Millisecond, 60*time.Millisecond))).Reply(mockhttp.OK()))
m.MustMock(mockhttp.Getf("/video").Pipe(fault.Bandwidth(fault.Bandwidth3G)).Reply(mockhttp.OK().BodyFile("video.mp4")))
```

A wire fault needs HTTP/1.x, as wire pipes do. Faults combine with other pipes: a fault on the wire comes after the pipes of `Pipe`. A mock that answers with a fault still counts the call, and runs its callbacks and events, before the connection ends.

## In mock files

`pipes`, `wire_pipes` and `upstream_pipes` list faults for `Pipe`, `WirePipe` and `UpstreamPipe`. A fault is an object: `type` names it, after its function in snake case, and its other keys are its parameters, by name. Durations are whole milliseconds or a Go duration, such as `1.5s`. A parameter with a default can be left out.

| `type` | Parameters |
|---|---|
| `empty_response`, `malformed_response_chunk`, `random_data_then_close`, `connection_reset_by_peer` | None. |
| `chunked_dribble_delay` | `chunks`, `total` |
| `latency` | `delay`: a duration or a [delay distribution](/more/mock-lifecycle#delays) |
| `bandwidth` | `rate`: bytes per second, or a `fault.Bandwidth*` constant in snake case: `gprs`, `dial_up`, `2g`, `good_2g`, `3g`, `good_3g`, `dsl`, `4g`, `wifi` or `fiber` |
| `slicer` | `average_size`; `size_variation` and `delay`, 0 by default |
| `slow_close` | `delay` |
| `timeout` | `after`, 0 by default: never |
| `limit_data` | `bytes` |
| `connection_reset_by_peer_after` | `after` |

```yaml
request:
  method: GET
  path: /report
response:
  status: 200
  body_file: report.csv
pipes:
  - type: bandwidth
    rate: 3g
  - type: connection_reset_by_peer_after
    after: 5s
wire_pipes:
  - type: latency
    delay:
      type: jitter
      latency: 300ms
      jitter: 60ms
```

A fault with a missing parameter, a key it doesn't take, or a value its function refuses fails the file, with each problem at its key, such as `pipes[1].after`. `faultbuild.Build`, of package `fault/faultbuild`, builds a fault from its object in Go.

A mock describes its faults the same way, so the admin API shows them and its store keeps them. A pipe of your own, such as a `vlatte.PipeFunc`, isn't in the description.
