package mockgrpc

import (
	"context"
	"maps"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	v1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	v1alpha "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

var (
	_ reflection.ServiceInfoProvider = (*reflectedServices)(nil)
	_ interface {
		FindFileByPath(string) (protoreflect.FileDescriptor, error)
		FindDescriptorByName(protoreflect.FullName) (protoreflect.Descriptor, error)
	} = (*resolver)(nil)
)

// registerReflection registers the reflection service, v1 and v1alpha, which older tools ask, as reflection.Register
// does. The server implements them itself: the mocks don't serve them.
func (app *Server) registerReflection() {
	opts := reflection.ServerOptions{Services: &reflectedServices{app: app}, DescriptorResolver: app.resolver}

	v1.RegisterServerReflectionServer(app.server, reflection.NewServerV1(opts))
	v1alpha.RegisterServerReflectionServer(app.server, reflection.NewServer(opts))

	app.internal[v1.ServerReflection_ServiceDesc.ServiceName] = true
	app.internal[v1alpha.ServerReflection_ServiceDesc.ServiceName] = true
}

// reflectedServices lists the services the server serves to reflection: those registered on it, those of its
// descriptors, and those its mocks name.
type reflectedServices struct {
	app *Server
}

// GetServiceInfo implements reflection.ServiceInfoProvider. Reflection reads the names only.
func (r *reflectedServices) GetServiceInfo() map[string]grpc.ServiceInfo {
	services := maps.Clone(r.app.server.GetServiceInfo())

	for _, files := range r.app.resolver.files {
		files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
			for i := range fd.Services().Len() {
				services[string(fd.Services().Get(i).FullName())] = grpc.ServiceInfo{}
			}

			return true
		})
	}

	mocks, _ := r.app.storage.PickAll(context.Background())
	for _, m := range mocks {
		for _, method := range m.Methods() {
			svc, _, _ := strings.Cut(strings.TrimPrefix(method, "/"), "/")
			services[svc] = grpc.ServiceInfo{}
		}
	}

	return services
}

// FindFileByPath implements protodesc.Resolver, for reflection: the file at path, in the server's descriptors first.
func (r *resolver) FindFileByPath(path string) (protoreflect.FileDescriptor, error) {
	for _, files := range r.registries() {
		if fd, err := files.FindFileByPath(path); err == nil {
			return fd, nil
		}
	}

	return nil, protoregistry.NotFound
}

// FindDescriptorByName implements protodesc.Resolver, for reflection: the descriptor of name, in the server's
// descriptors first.
func (r *resolver) FindDescriptorByName(name protoreflect.FullName) (protoreflect.Descriptor, error) {
	for _, files := range r.registries() {
		if d, err := files.FindDescriptorByName(name); err == nil {
			return d, nil
		}
	}

	return nil, protoregistry.NotFound
}

// isInternal reports whether the service of the full method is one the server implements itself.
func (app *Server) isInternal(method string) bool {
	svc, _, _ := strings.Cut(strings.TrimPrefix(method, "/"), "/")
	return app.internal[svc]
}
