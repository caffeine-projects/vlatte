package main

import (
	"io"

	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/webhook"
)

// settings are the command's own settings, which it reads from the sources the mock server reads: the server
// accepts and ignores them, as a library server must not serve an admin API settings ask for.
type settings struct {
	admin     bool
	adminAddr string
	adminDir  string
	mcp       bool
}

// defaultSettings serve the admin API, over MCP too, which keeps the mocks it adds across restarts: settings apply
// after them.
func defaultSettings() settings {
	return settings{admin: true, adminAddr: adminAddr(), adminDir: _adminDir, mcp: true}
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
				s.adminAddr = v.Str
				_, err := admin.New(admin.Addr(v.Str))

				return err
			},
		},
		config.Field[settings]{
			Key: "admin.dir", Kind: config.String,
			Usage: "Directory of the files that keep the mocks the admin API adds across restarts, which only the server " +
				"loads: keep it out of mock_files. Relative paths start at root_dir, and {workdir} at workdir. Empty keeps them " +
				"in memory.",
			Set: func(s *settings, v config.Value) error { s.adminDir = v.Str; return nil },
		},
		config.Field[settings]{
			Key: "mcp", Kind: config.Bool, Default: "true",
			Usage: "Serve the admin API's operations to AI agents over MCP: at POST /mcp of the admin API, and over stdio " +
				"with vlatte mcp.",
			Set: func(s *settings, v config.Value) error { s.mcp = v.Bool; return nil },
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
	cb, api := s.setup(mockhttp.Setup())

	m, err := mockhttp.New(cb.
		Journal(_journalDir).
		LogOutput(logOutput).
		ConfigSources(mockhttp.DefaultConfigSources(args)...).
		OnCompleteListener("webhook", wh))

	return m, api, err
}

// setup applies s to the server's setup, and returns the admin API it adds, if any.
func (s settings) setup(cb *mockhttp.ConfigBuilder) (*mockhttp.ConfigBuilder, *admin.Admin) {
	if !s.admin {
		return cb, nil
	}

	options := []admin.Option{admin.Addr(s.adminAddr)}
	if s.mcp {
		options = append(options, admin.MCP())
	}

	api := admin.MustNew(options...)
	cb.Admin(api)

	if s.adminDir != "" {
		cb.AdminStore(mockhttp.FileStore(s.adminDir))
	}

	return cb, api
}
