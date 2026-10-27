// Package mockgrpc runs gRPC mock servers for tests, and for the vlatte command.
//
// A server serves any method of the services whose generated code the program imports, or whose descriptor sets it
// was given with [ConfigBuilder.Descriptors]: no service needs registering. Mocks name methods, match the calls, and
// answer them, whatever their call type: unary, server, client or bidi streaming.
//
//	m := mockgrpc.MustNew(mockgrpc.Setup().InMemory())
//	m.MustStart()
//	t.Cleanup(m.Close)
//
//	m.MustMock(mockgrpc.Method("greeter.Greeter/SayHello").
//		Field("name", match.Equal("ann")).
//		Reply(mockgrpc.Message(&pb.HelloReply{Message: "hi ann"})))
//
//	client := pb.NewGreeterClient(m.MustDial())
//
// [Method] starts a mock; its matchers read the call's metadata and its request, as protojson writes it with the
// field names of the .proto file. [Message], [Messages], [JSON], [Template], [Status], [Bidi], [Echo] and [Func]
// reply, [Seq] and [Rand] answer with other replies in turn or at random, [Shared] gives mocks one Replier, and
// [From] answers with what another server answers, which [Server.StartRecording] records as mock files.
// Mocks can be limited with Times, take part in scenarios, wait with Delay, and run on_complete events, such as
// webhooks. A server can keep a journal of its calls, which tests verify with the same builders, and serve an admin
// API. Mocks can also come from JSON or YAML files: see [FromFile].
package mockgrpc
