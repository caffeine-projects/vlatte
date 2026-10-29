// Command vlatte runs a mock server: it serves the mock files under testdata/vlatte/_mocks in the working directory,
// and an admin API on an address of its own.
//
// Usage:
//
//	vlatte [flags]
//	vlatte <command> [flags]
//
// Without --addr, it listens on a random port and prints the URL. Settings can also come from a .vlatterc file or
// VLATTE_* environment variables: run 'vlatte --help' to list them. It can proxy and record a service, and serves the
// recordings on the next run. When a setting of the grpc section is set, such as --grpc-descriptors, it serves gRPC
// mocks too.
//
// The commands are:
//
//	completion  generate the shell completion script
//	health      check that a running server serves, as a container health check
//	mcp         serve the admin API to an AI agent over MCP, on stdin and stdout
//	version     print version info
//
// It exits with 0 on success, 2 for invalid settings or usage, and 1 for other errors.
package main
