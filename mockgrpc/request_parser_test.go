package mockgrpc

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

var (
	_ RequestParser = (*sealParser)(nil)
	_ RequestParser = (*funcParser)(nil)
)

func TestRequestParser_ACallNoParserCanParseIsReadAsTheClientSentIt(t *testing.T) {
	// A server with parsers reads a call none of them can parse as a server without parsers does: Field and templates
	// read protojson's names, enums by name and int64 values as text, and no parser is given the message.
	p := newSealParser(newSealer(t))
	m := parsing(t, p)
	m.MustMock(Method("TestService/Inspect").
		Field("color", match.Equal("COLOR_BLUE")).
		Field("count", match.Equal("42")).
		Reply(Template(`{"user_id": "{{ .Request.Message.user_id }}", "tags": ["{{ .Request.Message.count }}"]}`)))

	res, err := client(t, m).Inspect(t.Context(), &pb.Rich{UserId: "u1", Count: 42, Color: pb.Color_COLOR_BLUE})
	require.NoError(t, err)
	require.Equal(t, "u1", res.GetUserId())
	require.Equal(t, []string{"42"}, res.GetTags())
	require.Zero(t, p.total())
}

func TestRequestParser_TheFirstParserThatCanParseAMessageParsesIt(t *testing.T) {
	// Parsers are asked in the order they were added, across options, and the first that can parse a message parses
	// it: different metadata and methods get different parsers, and a later parser that could parse a message never
	// gets it.
	byTeam := &funcParser{
		can: func(call *Call, _ proto.Message) bool {
			return slices.Equal(call.Header().Get("x-team"), []string{"a"})
		},
		parse: func(context.Context, *Call, proto.Message) (any, error) {
			return map[string]any{"by": "team"}, nil
		},
	}
	byMethod := &funcParser{
		can: func(call *Call, _ proto.Message) bool {
			return call.Method() == pb.TestService_Greetings_FullMethodName
		},
		parse: func(context.Context, *Call, proto.Message) (any, error) {
			return map[string]any{"by": "method"}, nil
		},
	}
	last := &funcParser{
		parse: func(context.Context, *Call, proto.Message) (any, error) {
			return map[string]any{"by": "last"}, nil
		},
	}

	m := MustNew(
		Setup().Silent().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}).RequestParsers(byTeam),
		Setup().RequestParsers(byMethod, last),
	)
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Method("Greetings").Field("by", match.Equal("team")).Reply(JSON(`{"message": "team"}`)),
		Method("Greetings").Field("by", match.Equal("method")).Reply(JSON(`{"message": "method"}`)),
		Method("TestService/Inspect").Field("by", match.Equal("last")).Reply(JSON(`{"user_id": "last"}`)),
	)

	c := client(t, m)

	res, err := c.Greetings(metadata.AppendToOutgoingContext(t.Context(), "x-team", "a"), &pb.HiReq{})
	require.NoError(t, err)
	require.Equal(t, "team", res.GetMessage())

	res, err = c.Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)
	require.Equal(t, "method", res.GetMessage())

	rich, err := c.Inspect(t.Context(), &pb.Rich{})
	require.NoError(t, err)
	require.Equal(t, "last", rich.GetUserId())

	require.Equal(t, []int64{1, 1, 1}, []int64{byTeam.parsed.Load(), byMethod.parsed.Load(), last.parsed.Load()})
}

func TestRequestParser_WhatAParserReadsChoosesTheMock(t *testing.T) {
	// Field and templates read the inner message the parser opened, as protojson writes it, enums by name and int64
	// values as text: envelopes of different messages get different mocks.
	s := newSealer(t)
	m := parsing(t, newSealParser(s))
	m.MustMock(
		Method("Greetings").Field("user_id", match.Equal("u1")).Field("count", match.Equal("42")).
			Reply(Template(`{"message": "{{ .Request.Message.user_id }} {{ .Request.Message.color }}"}`)),
		Method("Greetings").Field("user_id", match.Equal("u2")).Reply(JSON(`{"message": "second"}`)),
	)

	c := client(t, m)

	res, err := c.Greetings(sealed(t.Context()), &pb.HiReq{Message: s.seal(t, &pb.Rich{UserId: "u1", Count: 42, Color: pb.Color_COLOR_RED})})
	require.NoError(t, err)
	require.Equal(t, "u1 COLOR_RED", res.GetMessage())

	res, err = c.Greetings(sealed(t.Context()), &pb.HiReq{Message: s.seal(t, &pb.Rich{UserId: "u2"})})
	require.NoError(t, err)
	require.Equal(t, "second", res.GetMessage())
}

func TestRequestParser_TwoEnvelopesOfOneMessageMatchTheSameMock(t *testing.T) {
	// Each seal of a message is new ciphertext: a mock matches what was sealed, whatever envelope carries it.
	s := newSealer(t)
	m := parsing(t, newSealParser(s))
	scope := m.MustMock(Method("Greetings").Field("user_id", match.Equal("u1")).Reply(JSON(`{"message": "u1"}`)))

	first, second := s.seal(t, &pb.Rich{UserId: "u1"}), s.seal(t, &pb.Rich{UserId: "u1"})
	require.NotEqual(t, first, second)

	for _, envelope := range []string{first, second} {
		res, err := client(t, m).Greetings(sealed(t.Context()), &pb.HiReq{Message: envelope})
		require.NoError(t, err)
		require.Equal(t, "u1", res.GetMessage())
	}

	require.Equal(t, int64(2), scope.Hits())
}

func TestRequestParser_TheRequestStaysAsTheClientSentIt(t *testing.T) {
	// Parsers change what Field, Body and templates read, not the request: Message, JSON and the call's own JSON and
	// Message still have the envelope, and Parsed what the parser read of it.
	s := newSealer(t)
	m := parsing(t, newSealParser(s))

	original := Func(func(_ context.Context, call *Call) error {
		inner, _ := call.Parsed()[0].(*pb.Rich)
		envelope, _ := call.Message().(*pb.HiReq)

		return call.Send(&pb.HiRes{Message: string(call.JSON()) + " " + envelope.GetMessage() + " " + inner.GetUserId()})
	})

	byMessage, byJSON := s.seal(t, &pb.Rich{UserId: "u1"}), s.seal(t, &pb.Rich{UserId: "u2"})
	m.MustMock(
		Method("Greetings").Message(&pb.HiReq{Message: byMessage}).Reply(original),
		Method("Greetings").JSON(`{"message": "`+byJSON+`"}`).Reply(original),
	)

	for envelope, user := range map[string]string{byMessage: "u1", byJSON: "u2"} {
		res, err := client(t, m).Greetings(sealed(t.Context()), &pb.HiReq{Message: envelope})
		require.NoError(t, err)
		require.Equal(t, `{"message":"`+envelope+`"} `+envelope+" "+user, res.GetMessage())
	}
}

func TestRequestParser_ParsesEachMessageOnce(t *testing.T) {
	// The server parses each message once, however many mocks, matchers, scenario keys and templates read it, so a
	// parser that decrypts doesn't decrypt again for each.
	s := newSealer(t)
	p := newSealParser(s)
	m := parsing(t, p)
	m.MustMock(
		Method("Greetings").Field("user_id", match.Equal("nobody")).Reply(JSON(`{}`)),
		Method("Greetings").Body(match.Contain("nobody")).Reply(JSON(`{}`)),
		Method("Greetings").Field("user_id", match.Equal("u1")).
			ScenarioIs("greet").ScenarioKey(`{{ .Request.Message.user_id }}`).
			Reply(Template(`{"message": "{{ .Request.Message.user_id }}"}`)),
		Method("Upload").Field("$[0].user_id", match.Equal("nobody")).Reply(JSON(`{}`)),
		Method("Upload").Field("$[0].user_id", match.Equal("u1")).Field("$[1].user_id", match.Equal("u2")).
			Reply(Template(`{"message": "{{ (index .Request.Message 1).user_id }}"}`)),
	)

	c := client(t, m)
	greeting, first, second := s.seal(t, &pb.Rich{UserId: "u1"}), s.seal(t, &pb.Rich{UserId: "u1"}), s.seal(t, &pb.Rich{UserId: "u2"})

	res, err := c.Greetings(sealed(t.Context()), &pb.HiReq{Message: greeting})
	require.NoError(t, err)
	require.Equal(t, "u1", res.GetMessage())

	stream, err := c.Upload(sealed(t.Context()))
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pb.ListItem{Key: first}))
	require.NoError(t, stream.Send(&pb.ListItem{Key: second}))

	up, err := stream.CloseAndRecv()
	require.NoError(t, err)
	require.Equal(t, "u2", up.GetMessage())

	require.Equal(t, []int{1, 1, 1}, []int{p.times(greeting), p.times(first), p.times(second)})
}

func TestRequestParser_ParsesTheRequestOfAStreamingServerCall(t *testing.T) {
	// The one message of a streaming server call is parsed before the call matches, as a unary call's is.
	s := newSealer(t)
	m := parsing(t, newSealParser(s))
	m.MustMock(Method("List").Field("user_id", match.Equal("u1")).Reply(Messages(&pb.ListItem{Key: "a"}, &pb.ListItem{Key: "b"})))

	stream, err := client(t, m).List(sealed(t.Context()), &pb.ListReq{Message: s.seal(t, &pb.Rich{UserId: "u1"})})
	require.NoError(t, err)

	var keys []string
	for {
		item, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}

		require.NoError(t, err)

		keys = append(keys, item.GetKey())
	}

	require.Equal(t, []string{"a", "b"}, keys)
}

func TestRequestParser_WhatAParserReturnsIsReadAsJSON(t *testing.T) {
	// What a parser returns is read as JSON: a message as protojson writes it, nil as null, raw JSON as it is, and any
	// other value as encoding/json writes it, map keys in order. A client stream reads each message as its own parser,
	// or none, read it: here the second message is no parser's.
	tests := []struct {
		name  string
		value any
		body  string
	}{
		{name: "message", value: &pb.HiReq{Message: "hi"}, body: `{"message":"hi"}`},
		{name: "nil message", value: (*pb.HiReq)(nil), body: `null`},
		{name: "nil", value: nil, body: `null`},
		{name: "raw JSON", value: jsontext.Value(`{"b": 1, "a": [true]}`), body: `{"b":1,"a":[true]}`},
		{name: "map", value: map[string]any{"b": int64(1) << 60, "a": "x"}, body: `{"a":"x","b":1152921504606846976}`},
		{name: "text", value: "hi", body: `"hi"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := parsing(t, &funcParser{
				can: func(_ *Call, msg proto.Message) bool { return envelopeText(msg) == "parsed" },
				parse: func(context.Context, *Call, proto.Message) (any, error) {
					return tt.value, nil
				},
			})
			m.MustMock(Method("Upload").Body(match.Equal(`[` + tt.body + `,{"key":"plain"}]`)).Reply(JSON(`{"message": "ok"}`)))

			got, code := upload(t, client(t, m), "parsed", "plain")
			require.Equal(t, codes.OK, code)
			require.Equal(t, "ok", got)
		})
	}
}

func TestRequestParser_ConcurrentCallsReadTheirOwnMessages(t *testing.T) {
	// One parser serves every call at once: each call reads what was parsed of its own message.
	s := newSealer(t)
	m := parsing(t, newSealParser(s))
	m.MustMock(Method("Greetings").Reply(Template(`{"message": "{{ .Request.Message.user_id }}"}`)))

	c := client(t, m)

	envelopes := make([]string, 16)
	want := make([]string, len(envelopes))
	for i := range envelopes {
		want[i] = strconv.Itoa(i)
		envelopes[i] = s.seal(t, &pb.Rich{UserId: want[i]})
	}

	got := make([]string, len(envelopes))
	errs := make([]error, len(envelopes))

	var wg sync.WaitGroup
	for i, envelope := range envelopes {
		wg.Go(func() {
			res, err := c.Greetings(sealed(t.Context()), &pb.HiReq{Message: envelope})
			got[i], errs[i] = res.GetMessage(), err
		})
	}
	wg.Wait()

	require.Equal(t, make([]error, len(envelopes)), errs)
	require.Equal(t, want, got)
}

func TestRequestParser_AParserThatFailsFailsTheCallAsAFailureToMatch(t *testing.T) {
	// No mock answers a message no parser could read: the call fails as one the server failed to match, with the
	// message that failed, so a test tells a parser's error from a missing mock. The parsers after the one that failed
	// aren't tried, nor the messages after the one that failed parsed.
	s := newSealer(t)
	sealing := newSealParser(s)
	rest := &funcParser{parse: func(context.Context, *Call, proto.Message) (any, error) {
		return map[string]string{"by": "rest"}, nil
	}}
	m := parsing(t, sealing, rest)
	greetings := m.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{})))
	uploads := m.MustMock(Method("Upload").Reply(Message(&pb.HiRes{})))
	c := client(t, m)

	var trailer metadata.MD
	_, err := c.Greetings(sealed(t.Context()), &pb.HiReq{Message: "not sealed"}, grpc.Trailer(&trailer))
	requireMatchFailure(t, err, trailer, "unary: "+pb.TestService_Greetings_FullMethodName, "message 0", "opening the envelope")
	require.Zero(t, rest.parsed.Load(), "the parsers after the one that failed aren't tried")

	third := s.seal(t, &pb.Rich{})
	stream, err := c.Upload(sealed(t.Context()))
	require.NoError(t, err)

	for _, key := range []string{s.seal(t, &pb.Rich{}), "not sealed", third} {
		require.NoError(t, stream.Send(&pb.ListItem{Key: key}))
	}

	_, err = stream.CloseAndRecv()
	requireMatchFailure(t, err, stream.Trailer(), "client streaming", "message 1", "opening the envelope")
	require.Zero(t, sealing.times(third), "the messages after the one that failed aren't parsed")
	require.Zero(t, greetings.Hits()+uploads.Hits())

	_, err = c.Greetings(t.Context(), &pb.HiReq{Message: "plain"})
	require.NoError(t, err, "a call the first parser can't parse is the next's")
	require.Equal(t, int64(1), rest.parsed.Load())
}

func TestRequestParser_AParsersStatusIsTheAnswer(t *testing.T) {
	// A parser that rejects a message with a gRPC status answers the call with it, as a real server rejecting the
	// message would: no failure trailer, and no mock counts the call. The journal tells the call failed.
	p := newSealParser(newSealer(t))
	p.reject = codes.InvalidArgument
	m, c := journaled(t, Setup().RequestParsers(p))
	scope := m.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{})))

	var trailer metadata.MD
	_, err := c.Greetings(sealed(t.Context()), &pb.HiReq{Message: "not sealed"}, grpc.Trailer(&trailer))
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "bad envelope", status.Convert(err).Message())
	require.Empty(t, trailer.Get(_failureKey))
	require.Zero(t, scope.Hits())

	e := onlyCall(t, m, journal.Query{})
	require.Equal(t, journal.OutcomeFailed, e.Outcome)
	require.Equal(t, int(codes.InvalidArgument), e.Status)
	require.Empty(t, e.MockID)
	require.Contains(t, e.Message, ErrParsing.Error())
}

func TestRequestParser_APanicOrAValueJSONCantWriteFailsTheCall(t *testing.T) {
	// A parser that panics, or returns what can't be read as JSON, fails the call as its error does: the server goes on.
	tests := []struct {
		name   string
		parser *funcParser
		want   string
	}{
		{
			name:   "CanParse panics",
			parser: &funcParser{can: func(*Call, proto.Message) bool { panic("can't tell") }},
			want:   "panic: can't tell",
		},
		{
			name: "Parse panics",
			parser: &funcParser{parse: func(context.Context, *Call, proto.Message) (any, error) {
				panic("boom")
			}},
			want: "panic: boom",
		},
		{
			name: "the value isn't JSON",
			parser: &funcParser{parse: func(context.Context, *Call, proto.Message) (any, error) {
				return make(chan int), nil
			}},
			want: "writing the parsed value as JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := parsing(t, tt.parser)
			scope := m.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{})))

			var trailer metadata.MD
			_, err := client(t, m).Greetings(t.Context(), &pb.HiReq{}, grpc.Trailer(&trailer))
			requireMatchFailure(t, err, trailer, tt.want)
			require.Zero(t, scope.Hits())
		})
	}
}

func TestRequestParser_AClientThatLeavesDuringTheParseAbandonsTheCall(t *testing.T) {
	// The parser reads the call's context: a client that leaves ends the parse, and the call is abandoned with the
	// code of its context, not failed as a parser's error would be.
	entered := make(chan struct{})
	p := &funcParser{parse: func(ctx context.Context, _ *Call, _ proto.Message) (any, error) {
		close(entered)
		<-ctx.Done()

		return nil, ctx.Err()
	}}
	m, c := journaled(t, Setup().RequestParsers(p))
	scope := m.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{})))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ended := make(chan error, 1)
	go func() {
		_, err := c.Greetings(ctx, &pb.HiReq{})
		ended <- err
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the parser never parsed")
	}

	cancel()
	require.Equal(t, codes.Canceled, status.Code(<-ended))
	require.True(t, m.VerifyRequests().Within(5*time.Second).Times(t, 1))

	e := onlyCall(t, m, journal.Query{})
	require.Equal(t, journal.OutcomeAbandoned, e.Outcome)
	require.Equal(t, int(codes.Canceled), e.Status)
	require.Zero(t, scope.Hits())
}

// sealer seals messages into the text an envelope carries, as an application that encrypts its payloads does: with
// AES-GCM, a key of its own and a new nonce for each seal, so two seals of one message differ.
type sealer struct {
	aead cipher.AEAD
}

func newSealer(t *testing.T) *sealer {
	t.Helper()

	key := make([]byte, 32)
	_, _ = rand.Read(key)

	block, err := aes.NewCipher(key)
	require.NoError(t, err)

	aead, err := cipher.NewGCMWithRandomNonce(block)
	require.NoError(t, err)

	return &sealer{aead: aead}
}

// seal returns msg sealed, as base64 text.
func (s *sealer) seal(t *testing.T, msg proto.Message) string {
	t.Helper()

	b, err := proto.Marshal(msg)
	require.NoError(t, err)

	return base64.StdEncoding.EncodeToString(s.aead.Seal(nil, nil, b, nil))
}

// open reads text, which seal returned, as the pb.Rich it sealed.
func (s *sealer) open(text string) (*pb.Rich, error) {
	b, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return nil, err
	}

	plain, err := s.aead.Open(nil, nil, b, nil)
	if err != nil {
		return nil, err
	}

	inner := new(pb.Rich)
	if err := proto.Unmarshal(plain, inner); err != nil {
		return nil, err
	}

	return inner, nil
}

// sealParser opens the envelopes of the calls whose x-sealed header is "v1": the pb.Rich the text of a request message
// carries, sealed. An envelope it can't open fails with its reject code, unless it is OK. It counts the envelopes it is
// given, by their text.
type sealParser struct {
	sealer *sealer
	reject codes.Code
	mu     sync.Mutex
	given  map[string]int
}

func newSealParser(s *sealer) *sealParser {
	return &sealParser{sealer: s, given: make(map[string]int)}
}

func (p *sealParser) CanParse(call *Call, _ proto.Message) bool {
	return slices.Equal(call.Header().Get("x-sealed"), []string{"v1"})
}

func (p *sealParser) Parse(_ context.Context, _ *Call, msg proto.Message) (any, error) {
	text := envelopeText(msg)

	p.mu.Lock()
	p.given[text]++
	p.mu.Unlock()

	inner, err := p.sealer.open(text)
	switch {
	case err == nil:
		return inner, nil
	case p.reject != codes.OK:
		return nil, status.Error(p.reject, "bad envelope")
	}

	return nil, fmt.Errorf("opening the envelope: %w", err)
}

// times returns how many times the parser was given the envelope text.
func (p *sealParser) times(text string) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.given[text]
}

// total returns how many envelopes the parser was given.
func (p *sealParser) total() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := 0
	for _, times := range p.given {
		n += times
	}

	return n
}

// envelopeText returns the text of msg, a request message of the test service: what an envelope seals.
func envelopeText(msg proto.Message) string {
	switch m := msg.(type) {
	case *pb.HiReq:
		return m.GetMessage()
	case *pb.ListReq:
		return m.GetMessage()
	case *pb.ListItem:
		return m.GetKey()
	}

	return ""
}

// sealed returns ctx with the metadata of a call whose envelopes a sealParser opens.
func sealed(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "x-sealed", "v1")
}

// opening returns a parser that opens the envelope of every message, as s sealed it: chat sends no header to tell
// sealed calls by.
func opening(s *sealer, rejected error) *funcParser {
	return &funcParser{parse: func(_ context.Context, _ *Call, msg proto.Message) (any, error) {
		inner, err := s.open(envelopeText(msg))
		if err != nil {
			return nil, rejected
		}

		return inner, nil
	}}
}

// funcParser is a RequestParser of functions: one without can parses every message. It counts the messages it parses.
type funcParser struct {
	can    func(call *Call, msg proto.Message) bool
	parse  func(ctx context.Context, call *Call, msg proto.Message) (any, error)
	parsed atomic.Int64
}

func (p *funcParser) CanParse(call *Call, msg proto.Message) bool {
	return p.can == nil || p.can(call, msg)
}

func (p *funcParser) Parse(ctx context.Context, call *Call, msg proto.Message) (any, error) {
	p.parsed.Add(1)
	return p.parse(ctx, call, msg)
}

// parsing returns a started server of the test service whose parsers read the requests.
func parsing(t *testing.T, parsers ...RequestParser) *Server {
	t.Helper()

	m := MustNew(Setup().Silent().
		Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}).
		RequestParsers(parsers...))
	m.MustStart()
	t.Cleanup(m.Close)

	return m
}
