// Package mockgrpc runs gRPC mock servers for tests.
//
// Register the service on a [Server], add mocks for its methods, then start it:
//
//	m := mockgrpc.MustNew()
//	pb.RegisterGreeterServer(m, pb.UnimplementedGreeterServer{})
//	m.MustStart()
//	defer m.Close()
//
//	m.MustMock(mockgrpc.UnaryMethod("Greeter/SayHello").
//		Reply(mockgrpc.Unary().Message(&pb.HelloReply{Message: "hi"})))
//
// Clients connect to [Server.Addr].
package mockgrpc
