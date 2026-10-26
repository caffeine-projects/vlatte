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
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

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
