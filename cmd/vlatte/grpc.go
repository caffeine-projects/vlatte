package main

import (
	"io"
	"os"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/mockgrpc"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/webhook"
)

const (
	// _grpcAddr is where the command's gRPC server listens, unless grpc.addr says.
	_grpcAddr = ":50051"

	// _dockerGRPCAdminPort is the port of the gRPC server's admin API in the vlatte image.
	_dockerGRPCAdminPort = "8082"

	// _grpcAdminDir is where the command keeps the mocks the gRPC server's admin API adds, across restarts, in the
	// HTTP server's work directory, next to the HTTP admin API's.
	_grpcAdminDir = mockhttp.WorkDirVar + "/_grpc_admin"
)

// newGRPC returns the command's gRPC server, as args and the shared sources set it, with the command's settings s,
// which logs to logOutput: it serves only when its settings turn the grpc section on. It sends the webhooks of mock
// files through wh and reads kv in templates, as the HTTP server does, serves reflection, and serves its admin API,
// which keeps the mocks it adds in the work directory of the HTTP server httpConf configures, and keeps a journal,
// unless settings turn them off.
func newGRPC(args []string, s settings, httpConf *mockhttp.Config, wh *webhook.WebHook, kv vlatte.KV, logOutput io.Writer) (*mockgrpc.Server, error) {
	addr := _grpcAddr
	if _, ok := os.LookupEnv(_dockerHostEnv); ok {
		addr = dockerHost() + strings.TrimPrefix(_grpcAddr, ":")
	}

	cb, err := s.setupGRPC(mockgrpc.Setup(), httpConf)
	if err != nil {
		return nil, err
	}

	return mockgrpc.New(cb.
		KV(kv).
		Addr(addr).
		Journal("").
		Reflection().
		LogOutput(logOutput).
		OnCompleteListener("webhook", wh).
		SkipDefaults().
		ConfigSources(mockgrpc.DefaultConfigSources(args)...))
}

// grpcAdminAddr is where the gRPC server's admin API listens unless settings say: a random local port, or its port of
// the vlatte image.
func grpcAdminAddr() string {
	if _, ok := os.LookupEnv(_dockerHostEnv); ok {
		return dockerHost() + _dockerGRPCAdminPort
	}

	return "127.0.0.1:0"
}
