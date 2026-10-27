package mockgrpc

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

// recorder returns a started server whose recordings go into a directory of the test, with options.
func recorder(t *testing.T, options ...Option) *Server {
	t.Helper()

	m := MustNew(append([]Option{Setup().Silent().RootDir(t.TempDir())}, options...)...)
	m.MustStart()
	t.Cleanup(m.Close)

	return m
}

// recordedTarget returns a server that answers every call type of the test service.
func recordedTarget(t *testing.T) *Server {
	t.Helper()

	target := serve(t)
	target.MustMock(
		Method("Greetings").Field("message", match.Equal("hi")).
			Reply(Message(&pb.HiRes{Message: "hello"}).Header("h", "v").Trailer("t", "w")),
		Method("Greetings").Field("message", match.Equal("who")).
			Reply(Status(codes.NotFound, "nobody").Details(&errdetails.ErrorInfo{Reason: "GONE"})),
		Method("List").Reply(Messages(&pb.ListItem{Key: "a"}, &pb.ListItem{Key: "b"}).Status(codes.DataLoss, "cut")),
		Method("Upload").Reply(Message(&pb.HiRes{Message: "uploaded"})),
		Method("Chat").Reply(Bidi().Otherwise(Message(&pb.HiRes{Message: "pong"}))),
	)

	return target
}

// answers is what a client got from every call type of the test service.
type answers struct {
	hello   string
	header  []string
	trailer []string
	nobody  *status.Status
	items   []string
	end     *status.Status
	upload  string
	chat    []string
}

// callAll calls every method of the test service on m, the way recordedTarget answers them.
func callAll(t *testing.T, m *Server) answers {
	t.Helper()

	c := client(t, m)

	var a answers

	var header, trailer metadata.MD
	res, err := c.Greetings(t.Context(), &pb.HiReq{Message: "hi"}, grpc.Header(&header), grpc.Trailer(&trailer))
	require.NoError(t, err)

	a.hello, a.header, a.trailer = res.GetMessage(), header.Get("h"), trailer.Get("t")

	_, err = c.Greetings(t.Context(), &pb.HiReq{Message: "who"})
	a.nobody = status.Convert(err)

	stream, err := c.List(t.Context(), &pb.ListReq{Message: "all"})
	require.NoError(t, err)

	for {
		item, err := stream.Recv()
		if err != nil {
			a.end = status.Convert(err)
			break
		}

		a.items = append(a.items, item.GetKey())
	}

	up, err := c.Upload(t.Context())
	require.NoError(t, err)
	require.NoError(t, up.Send(&pb.ListItem{Key: "x"}))

	uploaded, err := up.CloseAndRecv()
	require.NoError(t, err)

	a.upload = uploaded.GetMessage()

	var code codes.Code
	a.chat, code = chat(t, c, nil, "ping")
	require.Equal(t, codes.OK, code)

	return a
}

func TestRecording_PlaysBackWhatTheTargetAnswered(t *testing.T) {
	// A recording of a target answers the calls it recorded the way the target did, once the target is gone: every
	// call type, its metadata, messages and status.
	target := recordedTarget(t)
	m := recorder(t)

	require.NoError(t, m.StartRecording(RecordTarget(target.Addr())))
	require.True(t, m.IsRecording())

	live := callAll(t, m)

	res, err := m.StopRecording(t.Context())
	require.NoError(t, err)
	require.Len(t, res.Recordings, 5)
	require.Equal(t, int64(5), res.Status.Mocks)
	require.False(t, m.IsRecording())

	target.Close()

	played := callAll(t, m)
	require.Equal(t, answers{
		hello:   "hello",
		header:  []string{"v"},
		trailer: []string{"w"},
		nobody:  live.nobody,
		items:   []string{"a", "b"},
		end:     live.end,
		upload:  "uploaded",
		chat:    []string{"pong"},
	}, live)
	require.Equal(t, live.hello, played.hello)
	require.Equal(t, live.header, played.header)
	require.Equal(t, live.trailer, played.trailer)
	require.Equal(t, live.nobody.Proto().String(), played.nobody.Proto().String())
	require.Equal(t, live.items, played.items)
	require.Equal(t, live.end.Proto().String(), played.end.Proto().String())
	require.Equal(t, live.upload, played.upload)
	require.Equal(t, live.chat, played.chat)
}

func TestRecording_WritesAMockFileOfEachRequest(t *testing.T) {
	// A mock file of a recording reads as one written by hand: the method, the metadata the recording matches, and the
	// message, then the answer. The answers of a request that comes again are a sequence, whose last one repeats.
	target := serve(t)
	target.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "hello"}).Header("h", "v")).Once())
	target.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "again"})))

	m := recorder(t)
	require.NoError(t, m.StartRecording(RecordTarget(target.Addr()), RecordFormat("yaml"), RecordRequestHeaders(" X-Tenant ", "x-missing")))

	ctx := metadata.AppendToOutgoingContext(t.Context(), "x-tenant", "a")
	for range 2 {
		_, err := client(t, m).Greetings(ctx, &pb.HiReq{Message: "hi"})
		require.NoError(t, err)
	}

	res, err := m.StopRecording(t.Context())
	require.NoError(t, err)
	require.Len(t, res.Recordings, 1)
	require.Equal(t, int64(2), res.Status.Calls)

	rec := res.Recordings[0]
	require.Equal(t, "protobuf.TestService/Greetings", rec.Name)
	require.Equal(t, filepath.Join(m.Config().RootDir, "_mocks_recorded", rec.ID+".mock.yaml"), rec.File)

	b, err := os.ReadFile(rec.File)
	require.NoError(t, err)
	require.YAMLEq(t, `
id: `+rec.ID+`
name: protobuf.TestService/Greetings
request:
  method: /protobuf.TestService/Greetings
  header:
    x-tenant: [equal, a]
    x-missing: [not, present]
  message: {message: hi}
response_sequence:
  responses:
    - header: {h: [v]}
      message: {message: hello}
    - message: {message: again}
  after_ended:
    message: {message: again}
`, string(b))
}

func TestRecording_PlaysTheAnswersOfARepeatedRequestInTurn(t *testing.T) {
	// A target that answered a request differently each time plays back the same way: each answer in turn, then the
	// last one again, so a test sees a resource change as it did live.
	target := serve(t)
	target.MustMock(Method("Greetings").Reply(Seq(Message(&pb.HiRes{Message: "pending"}), Message(&pb.HiRes{Message: "done"}))))

	m := recorder(t)
	require.NoError(t, m.StartRecording(RecordTarget(target.Addr())))

	for range 2 {
		_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{Message: "job"})
		require.NoError(t, err)
	}

	res, err := m.StopRecording(t.Context())
	require.NoError(t, err)
	require.Len(t, res.Recordings, 1)
	require.Equal(t, 2, res.Recordings[0].Responses)

	target.Close()

	got := make([]string, 0, 3)
	for range 3 {
		answer, _ := greetWith(t, client(t, m), "job")
		got = append(got, answer)
	}

	require.Equal(t, []string{"pending", "done", "done"}, got)
}

func TestRecording_RecordRepeatsFirstKeepsTheFirstAnswer(t *testing.T) {
	target := serve(t)
	target.MustMock(Method("Greetings").Reply(Seq(Message(&pb.HiRes{Message: "first"}), Message(&pb.HiRes{Message: "second"}))))

	m := recorder(t)
	require.NoError(t, m.StartRecording(RecordTarget(target.Addr()), RecordRepeats(vlatte.RecordFirst)))

	for range 2 {
		_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{})
		require.NoError(t, err)
	}

	res, err := m.StopRecording(t.Context())
	require.NoError(t, err)
	require.Len(t, res.Recordings, 1)
	require.Equal(t, 1, res.Recordings[0].Responses)

	target.Close()

	got, _ := greetWith(t, client(t, m), "")
	require.Equal(t, "first", got)
}

func TestRecording_TakesWhatProxyRepliesAnswer(t *testing.T) {
	// Without a target, a recording takes the answers of mocks that proxy, and leaves the mocks' own out.
	target := serve(t)
	target.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "proxied"})))

	m := recorder(t)
	m.MustMock(
		Method("Greetings").Field("message", match.Equal("p")).Reply(From(target.Addr())),
		Method("Greetings").Reply(Message(&pb.HiRes{Message: "mocked"})),
	)

	require.NoError(t, m.StartRecording())

	for _, msg := range []string{"p", "m"} {
		_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{Message: msg})
		require.NoError(t, err)
	}

	res, err := m.StopRecording(t.Context())
	require.NoError(t, err)
	require.Len(t, res.Recordings, 1)

	b, err := os.ReadFile(res.Recordings[0].File)
	require.NoError(t, err)
	require.Contains(t, string(b), `"proxied"`)
}

func TestRecording_IncludeMockedTakesTheMocksAnswersToo(t *testing.T) {
	m := recorder(t)
	m.MustMock(
		Method("Greetings").Reply(Message(&pb.HiRes{Message: "mocked"}).Header("h", "v")),
		Method("List").Reply(Messages(&pb.ListItem{Key: "a"})),
	)

	require.NoError(t, m.StartRecording(RecordIncludeMocked(true), RecordMethods(" Greetings ")))

	_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{Message: "x"})
	require.NoError(t, err)

	stream, err := client(t, m).List(t.Context(), &pb.ListReq{})
	require.NoError(t, err)

	_, err = stream.Recv()
	require.NoError(t, err)

	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)

	res, err := m.StopRecording(t.Context())
	require.NoError(t, err)
	require.Len(t, res.Recordings, 1, "List isn't one of the methods")

	b, err := os.ReadFile(res.Recordings[0].File)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"id": "`+res.Recordings[0].ID+`",
		"name": "protobuf.TestService/Greetings",
		"request": {"method": "/protobuf.TestService/Greetings", "message": {"message": "x"}},
		"response": {"header": {"h": ["v"]}, "message": {"message": "mocked"}}
	}`, string(b))
}

func TestRecording_TransformsChangeTheMocksWritten(t *testing.T) {
	target := serve(t)
	target.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "secret"})))

	m := recorder(t)
	require.NoError(t, m.StartRecording(RecordTarget(target.Addr()), RecordTransform(func(r *RecordedMock) error {
		r.Responses[0].Message = map[string]any{"message": "redacted"}
		return nil
	})))

	_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)

	_, err = m.StopRecording(t.Context())
	require.NoError(t, err)

	target.Close()

	res, err := client(t, m).Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)
	require.Equal(t, "redacted", res.GetMessage())
}

func TestRecording_ATargetCallIsJournaledAsProxied(t *testing.T) {
	target := serve(t)
	target.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{})))

	m := recorder(t, Setup().Journal(""))
	require.NoError(t, m.StartRecording(RecordTarget(target.Addr())))

	_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{Message: "x"})
	require.NoError(t, err)

	page, err := m.Journal().Find(t.Context(), journal.Query{})
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	require.Equal(t, journal.OutcomeProxied, page.Entries[0].Outcome)
	require.JSONEq(t, `{"message": "x"}`, string(page.Entries[0].Body))
}

func TestRecording_ATargetThatCantBeReachedFailsTheCallUnrecorded(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := lis.Addr().String()
	require.NoError(t, lis.Close())

	m := recorder(t)
	require.NoError(t, m.StartRecording(RecordTarget(addr)))

	var trailer metadata.MD
	_, err = client(t, m).Greetings(t.Context(), &pb.HiReq{}, grpc.Trailer(&trailer))
	require.Equal(t, codes.Internal, status.Code(err))
	require.Equal(t, []string{vlatte.FailureReply}, trailer.Get(vlatte.HeaderFailure))

	res, err := m.StopRecording(t.Context())
	require.NoError(t, err)
	require.Empty(t, res.Recordings)
	require.Equal(t, int64(0), res.Status.Calls)
}

func TestRecording_OneRecordingRunsAtATime(t *testing.T) {
	m := recorder(t)

	_, err := m.StopRecording(t.Context())
	require.ErrorIs(t, err, vlatte.ErrNotRecording)
	require.Equal(t, vlatte.RecordStatus{}, m.RecordingStatus())

	require.NoError(t, m.StartRecording())
	require.ErrorIs(t, m.StartRecording(), vlatte.ErrRecording)
	require.True(t, m.RecordingStatus().Active)

	res, err := m.StopRecording(t.Context())
	require.NoError(t, err)
	require.Empty(t, res.Recordings)

	require.NoError(t, m.StartRecording())
}

func TestRecording_RefusesSettingsItCantRecordWith(t *testing.T) {
	m := recorder(t)

	for name, tc := range map[string]struct {
		option RecordConfigurer
		want   string
	}{
		"a format of no mock file": {RecordFormat("xml"), `recorder: format "xml" is not one of`},
		"no directory":             {RecordDir(""), "recorder: no directory to record into"},
		"an empty method":          {RecordMethods("Greetings", " "), "recorder: method 1 is empty"},
		"an empty header":          {RecordRequestHeaders(""), "recorder: request header 0 is empty"},
		"a target grpc can't read": {RecordTarget("dns:///%zz"), `recorder: reply_proxy: target "dns:///%zz"`},
		"an option that fails":     {failingRecordOption{}, "recorder: option at index 0 with type mockgrpc.failingRecordOption: no"},
	} {
		t.Run(name, func(t *testing.T) {
			err := m.StartRecording(tc.option)
			if tc.want == "" {
				require.NoError(t, err)

				_, err = m.StopRecording(t.Context())
				require.NoError(t, err)

				return
			}

			require.ErrorContains(t, err, tc.want)
			require.False(t, m.IsRecording())
		})
	}
}

func TestRecording_TheConfigRecordsFromStartToClose(t *testing.T) {
	// A server configured to record records from its start, and serves the recordings of its directory on its next
	// start, with its mock files. Close stops the recording without serving it: the server serves nothing more.
	target := serve(t)
	target.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "recorded"})))

	dir := t.TempDir()
	conf := Setup().Silent().RootDir(dir).Record(RecordTarget(target.Addr()), RecordDir("rec"))

	first := MustNew(conf)
	first.MustStart()
	require.True(t, first.IsRecording())

	_, err := client(t, first).Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)

	first.Close()
	target.Close()

	files, err := filepath.Glob(filepath.Join(dir, "rec", "*.mock.json"))
	require.NoError(t, err)
	require.Len(t, files, 1)

	// The next server records into the same directory, from a target that is gone: what it recorded answers first.
	second := MustNew(Setup().Silent().RootDir(dir).Record(RecordDir("rec")))
	second.MustStart()
	t.Cleanup(second.Close)

	res, err := client(t, second).Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)
	require.Equal(t, "recorded", res.GetMessage())
}

func TestRecording_AConfigWithBadSettingsFailsNew(t *testing.T) {
	_, err := New(Setup().Silent().Record(RecordFormat("xml")))
	require.ErrorContains(t, err, `server: recorder: format "xml"`)

	_, err = New(Setup().Silent().Record(RecordRepeats(3)))
	require.ErrorContains(t, err, "server: recorder: repeats 3 is not vlatte.RecordSequence or vlatte.RecordFirst")

	_, err = New(Setup().Silent().Record(failingRecordOption{}))
	require.ErrorContains(t, err, "config: record option at index 0 with type mockgrpc.failingRecordOption: no")
}

// failingRecordOption is a record option that fails.
type failingRecordOption struct{}

func (failingRecordOption) Apply(*RecordConfig) error { return errors.New("no") }

func TestRecording_StartsFromTheServersRecordSettings(t *testing.T) {
	target := serve(t)
	target.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "recorded"})))

	m := recorder(t, Setup().Record(&RecordConfig{Target: target.Addr(), Dir: "rec", Methods: []string{"Greetings"}, RequestHeaders: []string{"x-tenant"}}))
	_, err := m.StopRecording(t.Context())
	require.NoError(t, err)

	require.NoError(t, m.StartRecording(RecordDialOptions(grpc.WithUserAgent("rec"))))

	_, err = client(t, m).Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)

	res, err := m.StopRecording(t.Context())
	require.NoError(t, err)
	require.Len(t, res.Recordings, 1)
	require.Equal(t, filepath.Join(m.Config().RootDir, "rec"), filepath.Dir(res.Recordings[0].File))
}

func TestRecording_AStartEndsARecordingWhoseStopGaveUp(t *testing.T) {
	m := recorder(t)
	m.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "mocked"})))
	require.NoError(t, m.StartRecording(RecordIncludeMocked(true)))

	_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)

	rec := m.recording.Load()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = m.StopRecording(ctx)
	require.ErrorIs(t, err, context.Canceled)

	<-rec.session.Done()
	require.NoError(t, m.StartRecording())
	require.NotSame(t, rec, m.recording.Load())

	<-rec.served
	require.NoError(t, rec.playErr)
}

func TestRecording_AStopWaitsForTheStopThatEndsIt(t *testing.T) {
	m := recorder(t)
	require.NoError(t, m.StartRecording())

	// Another stop ends it.
	rec := m.recording.Load()
	rec.ended.Store(true)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	res, err := m.StopRecording(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NotNil(t, res)

	rec.ended.Store(false)

	_, err = m.StopRecording(t.Context())
	require.NoError(t, err)

	res, err = m.end(t.Context(), rec, true)
	require.NoError(t, err)
	require.NotNil(t, res)
}

func TestRecording_ARecordingThatCantBeServedSaysWhy(t *testing.T) {
	m := recorder(t)
	m.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "mocked"})))
	require.NoError(t, m.StartRecording(RecordIncludeMocked(true)))

	_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)

	dir := filepath.Join(m.Config().RootDir, "_mocks_recorded")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.mock.json"), []byte("{"), 0o600))

	res, err := m.StopRecording(t.Context())
	require.ErrorContains(t, err, "recorder: serving the recordings:")
	require.Len(t, res.Recordings, 1)
}

func TestRecording_ADirectoryThatCantBeMadeFailsTheRecording(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), nil, 0o600))

	m := recorder(t, Setup().RootDir(root))
	require.ErrorContains(t, m.StartRecording(RecordDir("file")), "recorder:")
	require.False(t, m.IsRecording())

	conf := MustNew(Setup().Silent().RootDir(root).Record(RecordDir("file")))
	t.Cleanup(conf.Close)
	require.ErrorContains(t, conf.Start(), "recorder:")
}

func TestRecording_AStopLetsTheCallsOnTheTargetFinish(t *testing.T) {
	// Stopping a recording stops what it takes, not the traffic it proxies: a stream to the target that runs when the
	// recording stops gets the rest of its answer.
	got := make(chan struct{})

	target := serve(t)
	target.MustMock(Method("Chat").Reply(Func(func(_ context.Context, call *Call) error {
		if _, err := call.Recv(); err != nil {
			return err
		}

		close(got)

		msg, err := call.Recv()
		if err != nil {
			return err
		}

		return call.Send(&pb.HiRes{Message: "after " + msg.(*pb.HiReq).GetMessage()})
	})))

	m := recorder(t)
	require.NoError(t, m.StartRecording(RecordTarget(target.Addr())))

	stream, err := client(t, m).Chat(t.Context())
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pb.HiReq{Message: "a"}))

	<-got

	_, err = m.StopRecording(t.Context())
	require.NoError(t, err)

	require.NoError(t, stream.Send(&pb.HiReq{Message: "stop"}))
	require.NoError(t, stream.CloseSend())

	res, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "after stop", res.GetMessage())

	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
}
