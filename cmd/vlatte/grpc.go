package main

import (
	"io"
	"os"
	"slices"
	"strings"

	"github.com/caffeine-projects/vlatte/mockgrpc"
	"github.com/caffeine-projects/vlatte/webhook"
)

const (
	// _grpcAddr is where the command's gRPC server listens, unless grpc.addr says.
	_grpcAddr = ":50051"

	// _dockerGRPCAdminPort is the port of the gRPC server's admin API in the vlatte image.
	_dockerGRPCAdminPort = "8082"
)

// splitArgs splits the command's arguments between its servers: the flags of gRPC settings, --grpc-*, go to the gRPC
// server, with their values, and the others to the HTTP server. The config file, --config or -c, goes to both: the
// gRPC server reads the grpc section of the file the HTTP server reads. The flags end at --, as the HTTP server's
// parser reads them: -- and what follows go to the HTTP server, which rejects them as unexpected arguments.
func splitArgs(args []string) (httpArgs, grpcArgs []string) {
	// The command reads some gRPC settings itself, such as grpc.admin: the gRPC server accepts them too.
	takesValue := make(map[string]bool)
	for _, f := range slices.Concat(mockgrpc.ConfigFlags(), settingsSchema().Flags()) {
		takesValue[f.Name] = !f.Bool
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "--" {
			httpArgs = append(httpArgs, args[i:]...)
			break
		}

		name, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if !strings.HasPrefix(arg, "-") {
			httpArgs = append(httpArgs, arg)
			continue
		}

		switch {
		case name == "c" || name == "config":
			httpArgs = append(httpArgs, arg)

			if !hasValue && i+1 < len(args) {
				i++
				value = args[i]
				httpArgs = append(httpArgs, value)
			}

			grpcArgs = append(grpcArgs, "--config="+value)
		case strings.HasPrefix(name, "grpc"):
			grpcArgs = append(grpcArgs, arg)

			if takesValue[name] && !hasValue && i+1 < len(args) {
				i++
				grpcArgs = append(grpcArgs, args[i])
			}
		default:
			httpArgs = append(httpArgs, arg)
		}
	}

	return httpArgs, grpcArgs
}

// newGRPC returns the command's gRPC server, as args and the shared sources set it, with the command's settings s,
// which logs to logOutput: it serves only when its settings turn the grpc section on. It sends the webhooks of mock
// files through wh, as the HTTP server does, serves reflection, and serves its admin API and keeps a journal, unless
// settings turn them off.
func newGRPC(args []string, s settings, wh *webhook.WebHook, logOutput io.Writer) (*mockgrpc.Server, error) {
	addr := _grpcAddr
	if _, ok := os.LookupEnv(_dockerHostEnv); ok {
		addr = dockerHost() + strings.TrimPrefix(_grpcAddr, ":")
	}

	return mockgrpc.New(s.setupGRPC(mockgrpc.Setup()).
		Addr(addr).
		Journal("").
		Reflection().
		LogOutput(logOutput).
		OnCompleteListener("webhook", wh).
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
