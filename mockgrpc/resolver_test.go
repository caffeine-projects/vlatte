package mockgrpc

import (
	"cmp"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/caffeine-projects/vlatte/match"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

// writeDescriptorSet writes the descriptors of the test service, in package pkg, as protoc --descriptor_set_out does
// without --include_imports: a service the program has no generated code of. It returns the path of the file.
func writeDescriptorSet(t *testing.T, pkg string) string {
	t.Helper()

	fd := protodesc.ToFileDescriptorProto(pb.File_test_proto)
	fd.Name = new(pkg + ".proto")
	fd.Package = new(pkg)

	// The types the file names are in the package too.
	rename := func(name *string) *string {
		if v, ok := strings.CutPrefix(*cmp.Or(name, new("")), ".protobuf."); ok {
			return new("." + pkg + "." + v)
		}

		return name
	}

	for _, msg := range fd.GetMessageType() {
		for _, f := range msg.GetField() {
			f.TypeName = rename(f.TypeName)
		}

		for _, nested := range msg.GetNestedType() {
			for _, f := range nested.GetField() {
				f.TypeName = rename(f.TypeName)
			}
		}
	}

	for _, svc := range fd.GetService() {
		for _, m := range svc.GetMethod() {
			m.InputType, m.OutputType = rename(m.InputType), rename(m.OutputType)
		}
	}

	b, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{fd}})
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), pkg+".binpb")
	require.NoError(t, os.WriteFile(path, b, 0o600))

	return path
}

func TestDescriptors_ServeAServiceWithoutItsGeneratedCode(t *testing.T) {
	// A server knew only the services whose generated code the program had registered: a mock file of any other one
	// couldn't be served. Given its descriptors, the server serves its calls, with messages of dynamic types.
	m := MustNew(Setup().Silent().Descriptors(writeDescriptorSet(t, "dyn")))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Method("dyn.TestService/Greetings").
		Field("message", match.Equal("hi")).
		Reply(JSON(`{"message": "dynamic"}`)))

	files, err := loadDescriptors([]string{writeDescriptorSet(t, "dyn")})
	require.NoError(t, err)

	desc := func(name string) protoreflect.MessageDescriptor {
		d, err := files.FindDescriptorByName(protoreflect.FullName(name))
		require.NoError(t, err)

		return d.(protoreflect.MessageDescriptor)
	}

	req := dynamicpb.NewMessage(desc("dyn.HiReq"))
	req.Set(req.Descriptor().Fields().ByName("message"), protoreflect.ValueOfString("hi"))
	res := dynamicpb.NewMessage(desc("dyn.HiRes"))

	require.NoError(t, m.MustDial().Invoke(t.Context(), "/dyn.TestService/Greetings", req, res))
	require.Equal(t, "dynamic", res.Get(res.Descriptor().Fields().ByName("message")).String())
}

func TestDescriptors_ASetThatDoesntLoadFailsNew(t *testing.T) {
	_, err := New(Setup().Descriptors(filepath.Join(t.TempDir(), "missing.binpb")))
	require.ErrorContains(t, err, "descriptors:")

	garbage := filepath.Join(t.TempDir(), "garbage.binpb")
	require.NoError(t, os.WriteFile(garbage, []byte("not a descriptor set"), 0o600))

	_, err = New(Setup().Descriptors(garbage))
	require.ErrorContains(t, err, "descriptors: "+garbage)
}

func TestDescriptors_ASetWithItsImportsOrTwiceLoadsOnce(t *testing.T) {
	// protoc --include_imports writes the well-known types the set imports, and a server given two sets that share a
	// file loads it once: neither is a duplicate.
	fd := protodesc.ToFileDescriptorProto(pb.File_test_proto)
	ts := protodesc.ToFileDescriptorProto(timestamppb.File_google_protobuf_timestamp_proto)

	b, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{ts, fd}})
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "full.binpb")
	require.NoError(t, os.WriteFile(path, b, 0o600))

	files, err := loadDescriptors([]string{path, path})
	require.NoError(t, err)
	require.Equal(t, 2, files.NumFiles())
}

func TestDescriptors_AnImportNoFileHasFailsNew(t *testing.T) {
	fd := &descriptorpb.FileDescriptorProto{
		Name:       new("lonely.proto"),
		Package:    new("lonely"),
		Dependency: []string{"nowhere.proto"},
		Syntax:     new("proto3"),
	}

	b, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{fd}})
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "lonely.binpb")
	require.NoError(t, os.WriteFile(path, b, 0o600))

	_, err = New(Setup().Descriptors(path))
	require.ErrorContains(t, err, "descriptors:")
	require.ErrorContains(t, err, "nowhere.proto")
}

func TestResolver_FindsTheTypesOfItsDescriptors(t *testing.T) {
	// protojson resolves the Any fields of a message with the server's resolver: a type of the server's descriptors
	// resolves as well as one of generated code, and an unknown one is protoregistry.NotFound, as protojson expects.
	files, err := loadDescriptors([]string{writeDescriptorSet(t, "dyn")})
	require.NoError(t, err)

	r := newResolverOf(files)

	mt, err := r.FindMessageByURL("type.googleapis.com/dyn.HiReq")
	require.NoError(t, err)
	require.Equal(t, protoreflect.FullName("dyn.HiReq"), mt.Descriptor().FullName())

	mt, err = r.FindMessageByName("protobuf.HiReq")
	require.NoError(t, err)
	require.Equal(t, protoreflect.FullName("protobuf.HiReq"), mt.Descriptor().FullName())

	_, err = r.FindMessageByName("dyn.Nope")
	require.ErrorIs(t, err, protoregistry.NotFound)

	_, err = r.FindExtensionByName("dyn.nope")
	require.ErrorIs(t, err, protoregistry.NotFound)

	_, err = r.FindExtensionByNumber("dyn.HiReq", 100)
	require.ErrorIs(t, err, protoregistry.NotFound)
}

func TestResolver_MethodOfANameNoServiceHasIsNone(t *testing.T) {
	r := newResolverOf()

	for _, name := range []string{"/protobuf.TestService/Greetings", "protobuf.TestService/Greetings"} {
		md, ok := r.method(name)
		require.True(t, ok, name)
		require.Equal(t, "/protobuf.TestService/Greetings", fullMethod(md))
	}

	for _, name := range []string{"Greetings", "/protobuf.HiReq/Greetings", "/protobuf.Nope/Greetings", "/protobuf.TestService/Nope"} {
		_, ok := r.method(name)
		require.False(t, ok, name)
	}
}

func TestResolver_EncodeFailsOnAMessageProtoJSONCantWrite(t *testing.T) {
	// The journal and the matchers read a call as JSON: a message protojson refuses, such as a string that isn't
	// UTF-8, fails the call rather than matching as something else.
	r := newResolverOf()

	_, err := r.encode(&pb.HiReq{Message: "\xff"})
	require.ErrorContains(t, err, "encoding the request message")

	_, err = r.encodeArray([]proto.Message{&pb.HiReq{}, &pb.HiReq{Message: "\xff"}})
	require.ErrorContains(t, err, "message 1: encoding the request message")
}

func TestCallType_NamesEachShape(t *testing.T) {
	// Errors name the call types of methods that differ, such as an ambiguous method's.
	require.Equal(t, "unary", _unary.String())
	require.Equal(t, "server streaming", _serverStream.String())
	require.Equal(t, "client streaming", _clientStream.String())
	require.Equal(t, "bidi streaming", _bidiStream.String())
	require.Equal(t, "unknown", callType(0).String())
}
