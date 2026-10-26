package main

import (
	"os"
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
	takesValue := make(map[string]bool)
	for _, f := range mockgrpc.ConfigFlags() {
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

// newGRPC returns the command's gRPC server, as args and the shared sources set it: it serves only when its settings
// turn the grpc section on. It sends the webhooks of mock files through wh, as the HTTP server does, serves
// reflection, and serves its admin API and keeps a journal, unless settings turn them off.
func newGRPC(args []string, wh *webhook.WebHook) (*mockgrpc.Server, error) {
	addr, admin := _grpcAddr, "127.0.0.1:0"
	if _, ok := os.LookupEnv(_dockerHostEnv); ok {
		addr, admin = dockerHost()+strings.TrimPrefix(_grpcAddr, ":"), dockerHost()+_dockerGRPCAdminPort
	}

	return mockgrpc.New(mockgrpc.Setup().
		Addr(addr).
		Admin(admin).
		Journal("").
		Reflection().
		OnCompleteListener("webhook", wh).
		ConfigSources(mockgrpc.DefaultConfigSources(args)...))
}
