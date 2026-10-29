// Package fault provides pipes that make a mock answer wrong, to test how a client handles a server that fails: no
// answer at all, a malformed one, garbage, a reset connection, a body that trickles in, a slow or distant network.
//
// A fault is a [vlatte.Pipe]. Most write what isn't HTTP, so they need the bytes of the whole answer: they go with
// [github.com/caffeine-projects/vlatte/mockhttp.Builder.WirePipe], over HTTP/1.x.
//
//	m.MustMock(mockhttp.Getf("/users").WirePipe(fault.ConnectionResetByPeer()).Reply(mockhttp.OK()))
//
// [ChunkedDribbleDelay] changes when the body arrives, not what it is: it goes with
// [github.com/caffeine-projects/vlatte/mockhttp.Builder.Pipe], over any protocol.
//
// Those that act as a bad network does go with either: [Latency], [Bandwidth], [Slicer], [SlowClose], [Timeout],
// [LimitData] and [ConnectionResetByPeerAfter]. With Pipe they act on the body; with WirePipe, on every byte of the
// answer, its head included. A reset with Pipe over HTTP/2 cuts the stream, as HTTP/2 has no connection of its own to
// reset.
//
//	m.MustMock(mockhttp.Getf("/report").Pipe(fault.Bandwidth(fault.Bandwidth3G)).Reply(mockhttp.OK().BodyFile("report.csv")))
//
// A fault that ends the answer aborts its flow: the mock still counts the call, and runs its callbacks and events,
// before the connection ends.
//
// Mock files declare faults as objects, such as {"type": "bandwidth", "rate": "3g"}, under pipes, wire_pipes and
// upstream_pipes: see [github.com/caffeine-projects/vlatte/fault/faultbuild]. Each fault describes itself as the
// object that builds it.
package fault
