package xgrpc

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

var _ vlatte.Flavor[*GRPCMock, *GRPCMockApp] = (*GRPCMockApp)(nil)

type GRPCMockApp struct {
	*vlatte.Base[*GRPCMock, *GRPCMockApp]

	server  *grpc.Server
	config  *Config
	logger  *zerolog.Logger
	addr    string
	storage vlatte.Store[*GRPCMock]
	served  chan struct{}
}

type ServerInfo struct {
	Addr string
}

// New creates a new GRPCMockApp mock server with the given configurations.
// It returns an error if a configuration fails to apply.
func New(config ...vlatte.Configurer[*Config]) (*GRPCMockApp, error) {
	app := &GRPCMockApp{}
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

	store := vlatte.NewStore[*GRPCMock]()
	in := &Interceptors{app: app}
	// User options come first: the mock interceptors never call the next handler, so they must run last.
	// Chain* is used because the plain interceptor options can only be set once.
	opts := append(slices.Clone(conf.ServerOptions),
		grpc.ChainUnaryInterceptor(in.UnaryInterceptor),
		grpc.ChainStreamInterceptor(in.StreamInterceptor),
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
func MustNew(config ...vlatte.Configurer[*Config]) *GRPCMockApp {
	app, err := New(config...)
	if err != nil {
		panic(err)
	}

	return app
}

// Start starts the mock server.
func (app *GRPCMockApp) Start() error {
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
func (app *GRPCMockApp) MustStart() {
	err := app.Start()
	if err != nil {
		panic(err)
	}
}

func (app *GRPCMockApp) Server() *grpc.Server {
	return app.server
}

func (app *GRPCMockApp) Addr() string {
	return app.addr
}

// Close stops the server, waiting for calls in flight to finish. It does nothing if the server was never started.
func (app *GRPCMockApp) Close() {
	if app.served == nil {
		return
	}

	app.server.GracefulStop()
	<-app.served
}

type grpcFlavor struct {
	opts []vlatte.Configurer[*Config]
}

// Flavor returns a factory for use with [vlatte.New].
//
//	m, err := vlatte.New(xgrpc.Flavor())
func Flavor(opts ...vlatte.Configurer[*Config]) vlatte.FlavorFactory[*GRPCMock, *GRPCMockApp] {
	return &grpcFlavor{opts: opts}
}

func (f *grpcFlavor) Create(_ context.Context) (*GRPCMockApp, error) {
	return New(f.opts...)
}
