package main

import (
	"io"

	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/httpx"
	"github.com/caffeine-projects/vlatte/mockgrpc"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/webhook"
)

// settings are the command's own settings, which it reads from the sources the mock server reads: the server
// accepts and ignores them, as a library server must not serve an admin API settings ask for.
type settings struct {
	admin       bool
	adminAddr   string
	adminDir    string
	mcp         bool
	docsVersion string // of both admin APIs

	grpcAdmin     bool // the gRPC server's admin API, which serves when the gRPC server does
	grpcAdminAddr string

	webhook *webhook.WebHook // the webhook listener the HTTP and gRPC servers share, if webhook.timeout is set
}

// defaultSettings serve the admin API, over MCP too, which keeps the mocks it adds across restarts, and the gRPC
// server's own: settings apply after them.
func defaultSettings() settings {
	return settings{
		admin: true, adminAddr: adminAddr(), adminDir: _adminDir, mcp: true,
		grpcAdmin: true, grpcAdminAddr: grpcAdminAddr(),
	}
}

func settingsSchema() *config.Schema[settings] {
	return config.NewSchema(
		// The file source reads the path the server's config setting names: the server documents it.
		config.Field[settings]{Key: "config", Short: "c", Kind: config.String, Hidden: true},
		config.Field[settings]{
			Key: "admin", Kind: config.Bool, Usage: "Serve the admin API, on an HTTP server of its own.", Default: "true",
			Set: func(s *settings, v config.Value) error { s.admin = v.Bool; return nil },
		},
		config.Field[settings]{
			Key: "admin.addr", Kind: config.String,
			Usage: "Address the admin API listens on: host:port, :port or port. Empty picks a random local port.",
			Set: func(s *settings, v config.Value) error {
				s.admin, s.adminAddr = true, v.Str
				_, err := admin.New(admin.Addr(v.Str))

				return err
			},
		},
		config.Field[settings]{
			Key: "admin.dir", Kind: config.String,
			Usage: "Directory of the files that keep the mocks the admin API adds across restarts, which only the server " +
				"loads: keep it out of mock_files. Relative paths start at root_dir, and {workdir} at workdir. Empty keeps them " +
				"in memory.",
			Set: func(s *settings, v config.Value) error { s.admin, s.adminDir = true, v.Str; return nil },
		},
		config.Field[settings]{
			Key: "admin.docs_version", Kind: config.String,
			Usage: "Version of Scalar, such as 1.80.0, that the documentation page of the admin APIs, at / and /docs, " +
				"loads instead of the one vlatte pins, which it checks against its hash. Empty is the pinned one.",
			Set: func(s *settings, v config.Value) error {
				s.admin, s.docsVersion = true, v.Str
				_, err := admin.New(admin.DocsVersion(v.Str))

				return err
			},
		},
		config.Field[settings]{
			Key: "mcp", Kind: config.Bool, Default: "true",
			Usage: "Serve the admin API's operations to AI agents over MCP: at POST /mcp of the admin API, and over stdio " +
				"with vlatte mcp.",
			Set: func(s *settings, v config.Value) error { s.mcp = v.Bool; return nil },
		},
		config.Field[settings]{
			Key: "webhook.timeout", Kind: config.Duration,
			Usage: "How long a webhook without a timeout of its own waits for its answer, for HTTP and gRPC mocks: " +
				"milliseconds or a Go duration (1.5s). 0 uses " + httpx.DefaultWebhookTimeout.String() + ".",
			Set: func(s *settings, v config.Value) error {
				var err error
				s.webhook, err = webhook.New(webhook.WithTimeout(v.Dur))

				return err
			},
		},
		config.Field[settings]{
			Key: "grpc.admin", Kind: config.Bool, Default: "true",
			Usage: "Serve the admin API of the gRPC server, on an HTTP server of its own.",
			Set:   func(s *settings, v config.Value) error { s.grpcAdmin = v.Bool; return nil },
		},
		config.Field[settings]{
			Key: "grpc.admin.addr", Kind: config.String,
			Usage: "Address the gRPC server's admin API listens on: host:port, :port or port. Empty picks a random local port.",
			Set: func(s *settings, v config.Value) error {
				s.grpcAdmin, s.grpcAdminAddr = true, v.Str
				_, err := admin.New(admin.Addr(v.Str))

				return err
			},
		},
	)
}

// loadSettings reads the command's settings from the sources of args, as the server reads its own.
func loadSettings(args []string) (settings, error) {
	s := defaultSettings()
	_, err := settingsSchema().LoadOnly(&s, mockhttp.DefaultConfigSources(args))

	return s, err
}

// newServer creates the server of args, with the command's settings s, whose mock files name wh as an on_complete
// listener, and which logs to logOutput. The command serves the admin API, which keeps the mocks it adds across
// restarts, and keeps a journal of the requests it serves, unless settings turn them off: they apply after code. It
// returns the admin API too, nil when it is off.
func newServer(args []string, s settings, wh *webhook.WebHook, logOutput io.Writer) (*mockhttp.Server, *admin.Admin, error) {
	cb, api := s.setupHTTP(mockhttp.Setup())

	m, err := mockhttp.New(cb.
		Journal(_journalDir).
		LogOutput(logOutput).
		ConfigSources(mockhttp.DefaultConfigSources(args)...).
		OnCompleteListener("webhook", wh))

	return m, api, err
}

// setupHTTP applies s to the HTTP server's setup, and returns the admin API it adds, if any.
func (s settings) setupHTTP(cb *mockhttp.ConfigBuilder) (*mockhttp.ConfigBuilder, *admin.Admin) {
	if !s.admin {
		return cb, nil
	}

	api := s.newAdmin(s.adminAddr)
	cb.Admin(api)

	if s.adminDir != "" {
		cb.AdminStore(mockhttp.FileStore(s.adminDir))
	}

	return cb, api
}

// setupGRPC applies s to the gRPC server's setup.
func (s settings) setupGRPC(cb *mockgrpc.ConfigBuilder) *mockgrpc.ConfigBuilder {
	if s.grpcAdmin {
		cb.Admin(s.newAdmin(s.grpcAdminAddr))
	}

	return cb
}

// newAdmin returns an admin API on addr, which serves MCP too unless s turns it off.
func (s settings) newAdmin(addr string) *admin.Admin {
	options := []admin.Option{admin.Addr(addr), admin.DocsVersion(s.docsVersion)}
	if s.mcp {
		options = append(options, admin.MCP())
	}

	return admin.MustNew(options...)
}
