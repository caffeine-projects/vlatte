// Package fault provides steps that make a mock answer wrong, to test how a client handles a server that fails: no
// answer at all, a malformed one, garbage, a reset connection, a body that trickles in, a slow or distant network.
//
// A fault is a [vlatte.Step]. Most write what isn't HTTP, so they need the bytes of the whole answer: they go with
// [github.com/caffeine-projects/vlatte/mockhttp.Builder.WireStep], over HTTP/1.x.
//
//	m.MustMock(mockhttp.Getf("/users").WireStep(fault.ConnectionResetByPeer()).Reply(mockhttp.OK()))
//
// [ChunkedDribbleDelay] changes when the body arrives, not what it is: it goes with
// [github.com/caffeine-projects/vlatte/mockhttp.Builder.Step], over any protocol.
//
// Those that act as a bad network does go with either: [Latency], [Bandwidth], [Slicer], [SlowClose], [Timeout],
// [LimitData] and [ConnectionResetByPeerAfter]. With Step they act on the body; with WireStep, on every byte of the
// answer, its head included. A reset with Step over HTTP/2 cuts the stream, as HTTP/2 has no connection of its own to
// reset.
//
//	m.MustMock(mockhttp.Getf("/report").Step(fault.Bandwidth(fault.Bandwidth3G)).Reply(mockhttp.OK().BodyFile("report.csv")))
//
// A fault that ends the answer aborts its flow: the mock still counts the call, and runs its callbacks and events,
// before the connection ends.
//
// Mock files declare faults as specs, such as ["bandwidth", "3g"], under steps, wire_steps and upstream_steps: see
// [github.com/caffeine-projects/vlatte/fault/faultbuild]. Each fault describes itself as its spec.
package fault
