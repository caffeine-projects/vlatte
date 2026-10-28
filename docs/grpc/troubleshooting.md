# Troubleshooting

## Failures

A gRPC server answers `NotFound` when no mock matches (`Setup().NoMatchCode` sets another), `Unimplemented` for a method no descriptor describes, and `Internal` when serving a call fails, with the same values in the `vlatte-error` trailer. A mock that answers with a status, as `Status(...)` does, gets no trailer: that status is its answer.

A mock that matched and failed, such as a template that fails or a call past the end of a `Seq`, answers with the `vlatte-error` trailer.

## Debug metadata

A gRPC server sends `vlatte-matched-mock-id`, `vlatte-matched-mock-name` and `vlatte-matched-mock-hits` as header metadata, with a mock's message or status, and `vlatte-request-id` when it keeps a journal. `Name(...)` on a mock builder sets the name. `mockgrpc.Setup().NoDebugHeaders()` turns them off.

## Shutdown

`Close` waits for the calls in flight up to `Setup().ShutdownTimeout(d)`, 5s by default, then cuts them.
