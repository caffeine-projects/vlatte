package mockgrpc_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockgrpc"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

var _ mockgrpc.RequestParser = wrapped{}

// The docs site shows Example from here to the end of the region.
// #region example

func Example() {
	m := mockgrpc.MustNew(mockgrpc.Setup().Silent().InMemory())
	m.MustStart()
	defer m.Close()

	m.MustMock(mockgrpc.Method("TestService/Greetings").
		Field("message", match.Equal("hi")).
		Reply(mockgrpc.Message(&pb.HiRes{Message: "hello"})))

	res, err := pb.NewTestServiceClient(m.MustDial()).Greetings(context.Background(), &pb.HiReq{Message: "hi"})
	if err != nil {
		panic(err)
	}

	fmt.Println(res.GetMessage())
	// Output: hello
}

// #endregion example

func ExampleMessages() {
	m := mockgrpc.MustNew(mockgrpc.Setup().Silent().InMemory())
	m.MustStart()
	defer m.Close()

	// A server stream answers each message in turn, then ends with the status.
	m.MustMock(mockgrpc.Method("TestService/List").
		Reply(mockgrpc.Messages(&pb.ListItem{Key: "a"}, &pb.ListItem{Key: "b"}).Status(codes.Unavailable, "gone")))

	stream, err := pb.NewTestServiceClient(m.MustDial()).List(context.Background(), &pb.ListReq{})
	if err != nil {
		panic(err)
	}

	for {
		item, err := stream.Recv()
		if err != nil {
			fmt.Println(status.Code(err))
			break
		}

		fmt.Println(item.GetKey())
	}
	// Output:
	// a
	// b
	// Unavailable
}

func ExampleBidi() {
	m := mockgrpc.MustNew(mockgrpc.Setup().Silent().InMemory())
	m.MustStart()
	defer m.Close()

	m.MustMock(mockgrpc.Method("TestService/Chat").Reply(mockgrpc.Bidi().
		On(match.Field("message", match.Equal("hi")), mockgrpc.Message(&pb.HiRes{Message: "hello"})).
		Otherwise(mockgrpc.JSON(`{"message": "?"}`)).
		OnEnd(mockgrpc.Message(&pb.HiRes{Message: "bye"}))))

	stream, err := pb.NewTestServiceClient(m.MustDial()).Chat(context.Background())
	if err != nil {
		panic(err)
	}

	for _, msg := range []string{"hi", "what"} {
		if err := stream.Send(&pb.HiReq{Message: msg}); err != nil {
			panic(err)
		}

		res, err := stream.Recv()
		if err != nil {
			panic(err)
		}

		fmt.Println(res.GetMessage())
	}

	_ = stream.CloseSend()

	for {
		res, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}

		fmt.Println(res.GetMessage())
	}
	// Output:
	// hello
	// ?
	// bye
}

func ExampleServer_VerifyRequests() {
	m := mockgrpc.MustNew(mockgrpc.Setup().Silent().InMemory().Journal(""))
	m.MustStart()
	defer m.Close()

	m.MustMock(mockgrpc.Method("TestService/Greetings").Reply(mockgrpc.Message(&pb.HiRes{})))

	client := pb.NewTestServiceClient(m.MustDial())
	for _, msg := range []string{"hi", "hi", "bye"} {
		if _, err := client.Greetings(context.Background(), &pb.HiReq{Message: msg}); err != nil {
			panic(err)
		}
	}

	// A mock builder is a pattern of the calls the journal kept.
	n, err := m.VerifyRequests().Matching(mockgrpc.Method("TestService/Greetings").Field("message", match.Equal("hi"))).Count(context.Background())
	if err != nil {
		panic(err)
	}

	fmt.Println(n)
	// Output: 2
}

// wrapped reads the message of a Greetings request as the request it wraps, as an application that wraps its payloads
// sends it: the protobuf bytes of a HiReq, as base64.
type wrapped struct{}

func (wrapped) CanParse(call *mockgrpc.Call, _ proto.Message) bool {
	return call.Method() == pb.TestService_Greetings_FullMethodName
}

func (wrapped) Parse(_ context.Context, _ *mockgrpc.Call, msg proto.Message) (any, error) {
	b, err := base64.StdEncoding.DecodeString(msg.(*pb.HiReq).GetMessage())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "the message isn't base64")
	}

	inner := new(pb.HiReq)
	if err := proto.Unmarshal(b, inner); err != nil {
		return nil, status.Error(codes.InvalidArgument, "the message wraps no HiReq")
	}

	return inner, nil
}

func ExampleRequestParser() {
	m := mockgrpc.MustNew(mockgrpc.Setup().Silent().InMemory().RequestParsers(wrapped{}))
	m.MustStart()
	defer m.Close()

	// Field reads what the parser read; the call keeps the message the client sent.
	m.MustMock(mockgrpc.Method("TestService/Greetings").
		Field("message", match.Equal("who")).
		Reply(mockgrpc.Func(func(_ context.Context, call *mockgrpc.Call) error {
			inner := call.Parsed()[0].(*pb.HiReq).GetMessage()
			envelope := call.Message().(*pb.HiReq).GetMessage()

			return call.Send(&pb.HiRes{Message: "hello " + inner + ", from " + envelope})
		})))

	b, err := proto.Marshal(&pb.HiReq{Message: "who"})
	if err != nil {
		panic(err)
	}

	req := &pb.HiReq{Message: base64.StdEncoding.EncodeToString(b)}

	res, err := pb.NewTestServiceClient(m.MustDial()).Greetings(context.Background(), req)
	if err != nil {
		panic(err)
	}

	fmt.Println(res.GetMessage())
	// Output: hello who, from CgN3aG8=
}
