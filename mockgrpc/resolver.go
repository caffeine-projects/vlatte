package mockgrpc

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

var (
	// ErrUnknownMethod is the error of a mock whose method no descriptor of the server describes: a typo, or a service
	// whose generated code the program doesn't import and whose descriptors the server wasn't given.
	ErrUnknownMethod = errors.New("mock: unknown method")

	// ErrAmbiguousMethod is the error of a mock whose method name matches methods of different call types, such as a
	// unary and a streaming one of two services: name the service too.
	ErrAmbiguousMethod = errors.New("mock: ambiguous method")
)

// callType is the shape of a method: how many messages each side sends.
type callType int

const (
	_unary callType = iota + 1
	_serverStream
	_clientStream
	_bidiStream
)

func (t callType) String() string {
	switch t {
	case _unary:
		return "unary"
	case _serverStream:
		return "server streaming"
	case _clientStream:
		return "client streaming"
	case _bidiStream:
		return "bidi streaming"
	}

	return "unknown"
}

// typeOf returns the call type of md.
func typeOf(md protoreflect.MethodDescriptor) callType {
	switch {
	case md.IsStreamingClient() && md.IsStreamingServer():
		return _bidiStream
	case md.IsStreamingClient():
		return _clientStream
	case md.IsStreamingServer():
		return _serverStream
	}

	return _unary
}

// resolver finds the descriptors of the methods a server serves: in the files it was given, then in
// protoregistry.GlobalFiles, where generated code registers its own.
type resolver struct {
	files []*protoregistry.Files // Config.Files and Config.Descriptors, searched first
	types []*dynamicpb.Types     // the message types of files, for those generated code doesn't have
}

// newResolverOf returns the resolver of files, searched before protoregistry.GlobalFiles.
func newResolverOf(files ...*protoregistry.Files) *resolver {
	r := &resolver{files: files}
	for _, f := range files {
		r.types = append(r.types, dynamicpb.NewTypes(f))
	}

	return r
}

// FindMessageByName implements protoregistry.MessageTypeResolver, for the Any fields of messages: generated types
// first, then those of the server's descriptors.
func (r *resolver) FindMessageByName(name protoreflect.FullName) (protoreflect.MessageType, error) {
	if mt, err := protoregistry.GlobalTypes.FindMessageByName(name); err == nil {
		return mt, nil
	}

	for _, t := range r.types {
		if mt, err := t.FindMessageByName(name); err == nil {
			return mt, nil
		}
	}

	return nil, protoregistry.NotFound
}

// FindMessageByURL implements protoregistry.MessageTypeResolver: url names the type after its last "/".
func (r *resolver) FindMessageByURL(url string) (protoreflect.MessageType, error) {
	name := url
	if _, after, ok := strings.CutLast(url, "/"); ok {
		name = after
	}

	return r.FindMessageByName(protoreflect.FullName(name))
}

// FindExtensionByName implements protoregistry.ExtensionTypeResolver.
func (r *resolver) FindExtensionByName(field protoreflect.FullName) (protoreflect.ExtensionType, error) {
	if xt, err := protoregistry.GlobalTypes.FindExtensionByName(field); err == nil {
		return xt, nil
	}

	for _, t := range r.types {
		if xt, err := t.FindExtensionByName(field); err == nil {
			return xt, nil
		}
	}

	return nil, protoregistry.NotFound
}

// FindExtensionByNumber implements protoregistry.ExtensionTypeResolver.
func (r *resolver) FindExtensionByNumber(message protoreflect.FullName, field protoreflect.FieldNumber) (protoreflect.ExtensionType, error) {
	if xt, err := protoregistry.GlobalTypes.FindExtensionByNumber(message, field); err == nil {
		return xt, nil
	}

	for _, t := range r.types {
		if xt, err := t.FindExtensionByNumber(message, field); err == nil {
			return xt, nil
		}
	}

	return nil, protoregistry.NotFound
}

// fullMethod is the name gRPC gives the method md: "/pkg.Service/Method".
func fullMethod(md protoreflect.MethodDescriptor) string {
	return "/" + string(md.Parent().FullName()) + "/" + string(md.Name())
}

// method returns the descriptor of the full method name, such as "/pkg.Service/Method", and false when none
// describes it.
func (r *resolver) method(name string) (protoreflect.MethodDescriptor, bool) {
	svc, method, ok := strings.Cut(strings.TrimPrefix(name, "/"), "/")
	if !ok {
		return nil, false
	}

	for _, files := range r.registries() {
		desc, err := files.FindDescriptorByName(protoreflect.FullName(svc))
		if err != nil {
			continue
		}

		sd, ok := desc.(protoreflect.ServiceDescriptor)
		if !ok {
			continue
		}

		if md := sd.Methods().ByName(protoreflect.Name(method)); md != nil {
			return md, true
		}
	}

	return nil, false
}

// find returns the descriptors of the methods name matches, by whole segments of their full method, as methodName
// matches: "Method", "Service/Method", "pkg.Service/Method" or the full method. They all have the same call type.
func (r *resolver) find(name string) ([]protoreflect.MethodDescriptor, error) {
	var (
		found []protoreflect.MethodDescriptor
		seen  = make(map[string]bool)
	)

	for _, files := range r.registries() {
		files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
			for i := range fd.Services().Len() {
				methods := fd.Services().Get(i).Methods()
				for j := range methods.Len() {
					md := methods.Get(j)
					full := fullMethod(md)
					if !seen[full] && namesMethod(name, full) {
						seen[full] = true
						found = append(found, md)
					}
				}
			}

			return true
		})
	}

	if len(found) == 0 {
		return nil, fmt.Errorf("%w %q: no service the server knows has it: import its generated code, or give the "+
			"server its descriptors with Setup().Descriptors", ErrUnknownMethod, name)
	}

	slices.SortFunc(found, func(a, b protoreflect.MethodDescriptor) int { return strings.Compare(fullMethod(a), fullMethod(b)) })

	for _, md := range found[1:] {
		if typeOf(md) != typeOf(found[0]) {
			return nil, fmt.Errorf("%w %q: %s is %s and %s is %s: name the service", ErrAmbiguousMethod, name,
				fullMethod(found[0]), typeOf(found[0]), fullMethod(md), typeOf(md))
		}
	}

	return found, nil
}

// registries returns the files to search, in order.
func (r *resolver) registries() []*protoregistry.Files {
	return append(slices.Clip(r.files), protoregistry.GlobalFiles)
}

// newMessage returns a new message of the type md describes: of its generated Go type, when the program has one, so
// replies and matchers get *pb.X, else a dynamic one.
func newMessage(md protoreflect.MessageDescriptor) proto.Message {
	if mt, err := protoregistry.GlobalTypes.FindMessageByName(md.FullName()); err == nil {
		return mt.New().Interface()
	}

	return dynamicpb.NewMessage(md)
}

// loadDescriptors reads the descriptor set files at paths, as protoc --descriptor_set_out or buf build write them, into
// one registry. A set should hold the files it imports (--include_imports): imports the set lacks resolve from the
// files of generated code.
func loadDescriptors(paths []string) (*protoregistry.Files, error) {
	var set descriptorpb.FileDescriptorSet

	seen := make(map[string]bool)

	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("descriptors: %w", err)
		}

		var fds descriptorpb.FileDescriptorSet
		if err := proto.Unmarshal(b, &fds); err != nil {
			return nil, fmt.Errorf("descriptors: %s: %w", path, err)
		}

		for _, fd := range fds.GetFile() {
			if !seen[fd.GetName()] {
				seen[fd.GetName()] = true
				set.File = append(set.File, fd)
			}
		}
	}

	files, err := protodesc.NewFiles(withGlobalImports(&set))
	if err != nil {
		return nil, fmt.Errorf("descriptors: %w", err)
	}

	return files, nil
}

// withGlobalImports adds to set the files it imports but lacks, from the files of generated code, such as the
// well-known types, so a set built without --include_imports still loads.
func withGlobalImports(set *descriptorpb.FileDescriptorSet) *descriptorpb.FileDescriptorSet {
	have := make(map[string]bool, len(set.GetFile()))
	for _, fd := range set.GetFile() {
		have[fd.GetName()] = true
	}

	var missing []*descriptorpb.FileDescriptorProto

	var add func(name string)
	add = func(name string) {
		if have[name] {
			return
		}

		fd, err := protoregistry.GlobalFiles.FindFileByPath(name)
		if err != nil {
			return
		}

		have[name] = true
		for i := range fd.Imports().Len() {
			add(fd.Imports().Get(i).Path())
		}

		missing = append(missing, protodesc.ToFileDescriptorProto(fd))
	}

	for _, fd := range set.GetFile() {
		for _, dep := range fd.GetDependency() {
			add(dep)
		}
	}

	if len(missing) == 0 {
		return set
	}

	return &descriptorpb.FileDescriptorSet{File: append(missing, set.GetFile()...)}
}

// encode returns msg as compact JSON, as matchers, the journal and templates read it: as protojson writes it, with
// the field names of the .proto file and zero values left out. protojson varies its spaces, which the journal
// shouldn't keep.
func (r *resolver) encode(msg proto.Message) ([]byte, error) {
	if msg == nil {
		return nil, nil
	}

	b, err := protojson.MarshalOptions{UseProtoNames: true, Resolver: r}.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("encoding the request message: %w", err)
	}

	v := jsontext.Value(b)
	if err := v.Compact(); err != nil {
		return nil, fmt.Errorf("encoding the request message: %w", err)
	}

	return v, nil
}

// encodeArray returns msgs as a compact JSON array, each as encode writes it.
func (r *resolver) encodeArray(msgs []proto.Message) ([]byte, error) {
	var buf bytes.Buffer

	buf.WriteByte('[')

	for i, msg := range msgs {
		if i > 0 {
			buf.WriteByte(',')
		}

		b, err := r.encode(msg)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}

		buf.Write(b)
	}

	buf.WriteByte(']')

	return buf.Bytes(), nil
}

// decode reads b, as protojson writes it, into a new message of the type desc describes. Field names may be those of
// the .proto file or their JSON names.
func (r *resolver) decode(desc protoreflect.MessageDescriptor, b []byte) (proto.Message, error) {
	msg := newMessage(desc)
	if err := (protojson.UnmarshalOptions{Resolver: r}).Unmarshal(b, msg); err != nil {
		return nil, fmt.Errorf("not a %s: %w", desc.FullName(), err)
	}

	return msg, nil
}
