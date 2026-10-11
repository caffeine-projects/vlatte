package mockgrpc

import (
	"context"
	"fmt"
	"net"
	"os"
	"slices"

	"github.com/caffeine-projects/vlatte"
	"github.com/rs/zerolog"
	"google.golang.org/grpc"
)

var (
	_ vlatte.Flavor[*Mock, *Server] = (*Server)(nil)
	_ grpc.ServiceRegistrar         = (*Server)(nil)
)

type Server struct {
	*vlatte.Base[*Mock, *Server]

	server  *grpc.Server
	config  *Config
	logger  *zerolog.Logger
	addr    string
	storage vlatte.Store[*Mock]
	served  chan struct{}
}

// New creates a new gRPC mock Server with the given configurations.
// It returns an error if a configuration fails to apply.
func New(config ...Option) (*Server, error) {
	app := &Server{}
	conf := defaultConfig()

	for i, configurer := range config {
		err := configurer.Apply(conf)
		if err != nil {
			return nil, fmt.Errorf(
				"server: error applying configuration at index %d with type %T\n%w",
				i,
				configurer,
				err,
			)
		}
	}

	store := vlatte.NewStore[*Mock]()
	in := &interceptors{app: app}
	// User options come first: the mock interceptors never call the next handler, so they must run last.
	// The recovery interceptors run just before them, so a panic serving a mock answers the call.
	// Chain* is used because the plain interceptor options can only be set once.
	opts := append(slices.Clone(conf.ServerOptions),
		grpc.ChainUnaryInterceptor(in.recoverUnary, in.UnaryInterceptor),
		grpc.ChainStreamInterceptor(in.recoverStream, in.StreamInterceptor),
	)
	srv := grpc.NewServer(opts...)

	if conf.ServiceDesc != nil && conf.Service != nil {
		srv.RegisterService(conf.ServiceDesc, conf.Service)
	}

	app.Base = vlatte.NewBase(app, store)
	app.storage = store
	app.config = conf
	app.server = srv
	app.logger = conf.Logger

	if app.logger == nil {
		l := zerolog.New(os.Stdout).With().Timestamp().Logger()
		app.logger = &l
	}

	return app, nil
}

// MustNew is like New but panics if any error occurs.
func MustNew(config ...Option) *Server {
	app, err := New(config...)
	if err != nil {
		panic(err)
	}

	return app
}

// Prep runs the given recipes on this mock server, in order.
// It stops at the first recipe that fails and returns its error.
func (app *Server) Prep(ctx context.Context, recipes ...Recipe) error {
	for i, recipe := range recipes {
		if err := recipe.Prep(ctx, &vlatte.RecipeInput[*Mock, *Server]{Flavor: app}); err != nil {
			return fmt.Errorf("server: error applying recipe at index %d with type %T\n%w", i, recipe, err)
		}
	}

	return nil
}

// Start starts the mock server.
func (app *Server) Start() error {
	lis := app.config.Listener
	if lis == nil {
		var err error
		lis, err = net.Listen("tcp", app.config.Addr)
		if err != nil {
			return fmt.Errorf("server: Start: failed to create listener: %w", err)
		}
	}

	app.addr = lis.Addr().String()
	app.served = make(chan struct{})

	go func(served chan struct{}) {
		defer close(served)

		// Serve returns nil after GracefulStop, so any error is a real failure.
		if err := app.server.Serve(lis); err != nil {
			app.logger.Error().Err(err).Msg("server: error serving")
		}
	}(app.served)

	return nil
}

// MustStart starts the mock server.
// It fails immediately if any error occurs.
func (app *Server) MustStart() {
	err := app.Start()
	if err != nil {
		panic(err)
	}
}

// GRPCServer returns the underlying gRPC server, for features beyond registering services,
// such as server reflection.
func (app *Server) GRPCServer() *grpc.Server {
	return app.server
}

// RegisterService registers a service on the mock server, so generated code can take it directly:
//
//	pb.RegisterGreeterServer(m, pb.UnimplementedGreeterServer{})
//
// It implements [grpc.ServiceRegistrar] and must be called before Start.
func (app *Server) RegisterService(desc *grpc.ServiceDesc, impl any) {
	app.server.RegisterService(desc, impl)
}

func (app *Server) Addr() string {
	return app.addr
}

// Close stops the server, waiting for calls in flight to finish. It does nothing if the server was never started.
func (app *Server) Close() {
	if app.served == nil {
		return
	}

	app.server.GracefulStop()
	<-app.served
}
