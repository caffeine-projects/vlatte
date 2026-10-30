package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/caffeine-projects/vlatte"
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

	// The credentials and the certificate of both admin APIs: one operator, one secret.
	username string
	password string
	token    string
	certs    []tls.Certificate // from admin.tls.cert and admin.tls.key; none serves plain HTTP

	grpcAdmin     bool // the gRPC server's admin API, which serves when the gRPC server does
	grpcAdminAddr string
	grpcAdminDir  string

	webhook *webhook.WebHook // the webhook listener the HTTP and gRPC servers share, if webhook.timeout is set
	kvFile  config.Value     // kv.file, the CSV file of the key-value store the HTTP and gRPC servers share
}

// defaultSettings serve the admin API, over MCP too, and the gRPC server's own, which keep the mocks they add across
// restarts: settings apply after them.
func defaultSettings() settings {
	return settings{
		admin: true, adminAddr: adminAddr(), adminDir: _adminDir, mcp: true,
		grpcAdmin: true, grpcAdminAddr: grpcAdminAddr(), grpcAdminDir: _grpcAdminDir,
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
			Usage: "Version of Scalar, such as 1.80.0, that the documentation page of the admin APIs, at /docs, " +
				"loads instead of the one vlatte pins, which it checks against its hash. Empty is the pinned one.",
			Set: func(s *settings, v config.Value) error {
				s.admin, s.docsVersion = true, v.Str
				_, err := admin.New(admin.DocsVersion(v.Str))

				return err
			},
		},
		config.Field[settings]{
			Key: "admin.auth.username", Kind: config.String,
			Usage: "Username the admin APIs ask every request for, but their probes, with admin.auth.password: HTTP Basic " +
				"authentication, which browsers show a sign-in prompt for. Serve them over TLS beyond the local host.",
			Set: func(s *settings, v config.Value) error { s.admin, s.username = true, v.Str; return nil },
		},
		config.Field[settings]{
			Key: "admin.auth.password", Kind: config.String, Secret: true,
			Usage: "Password of admin.auth.username. Or admin.auth.password_file.",
			Set:   func(s *settings, v config.Value) error { s.admin, s.password = true, v.Str; return nil },
		},
		config.Field[settings]{
			Key: "admin.auth.password_file", Kind: config.String,
			Usage: "File that holds the password of admin.auth.username, such as a Docker or Kubernetes secret. A " +
				"trailing line break isn't part of it.",
			Set: func(s *settings, v config.Value) error {
				var err error
				s.admin = true
				s.password, err = readSecret(v.Str)

				return err
			},
		},
		config.Field[settings]{
			Key: "admin.auth.token", Kind: config.String, Secret: true,
			Usage: "Bearer token the admin APIs ask every request for, but their probes: in Authorization: Bearer, or as " +
				"the password of Basic, with any username, from a browser. Generate one with openssl rand -hex 32. Or " +
				"admin.auth.token_file.",
			Set: func(s *settings, v config.Value) error { s.admin, s.token = true, v.Str; return nil },
		},
		config.Field[settings]{
			Key: "admin.auth.token_file", Kind: config.String,
			Usage: "File that holds the bearer token of the admin APIs, such as a Docker or Kubernetes secret. A " +
				"trailing line break isn't part of it.",
			Set: func(s *settings, v config.Value) error {
				var err error
				s.admin = true
				s.token, err = readSecret(v.Str)

				return err
			},
		},
		config.Field[settings]{
			Key: "admin.tls.cert", Kind: config.String,
			Usage: "Certificate file, PEM, the admin APIs serve HTTPS with, with admin.tls.key.",
			Set:   func(s *settings, _ config.Value) error { s.admin = true; return nil },
		},
		config.Field[settings]{
			Key: "admin.tls.key", Kind: config.String,
			Usage: "Private key file, PEM, of admin.tls.cert.",
			Set:   func(s *settings, _ config.Value) error { s.admin = true; return nil },
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
			Key: "kv.file", Kind: config.String,
			Usage: "CSV file to fill the key-value store with, which templates of HTTP and gRPC mocks read as .KV: the " +
				"header key,value, then a row for each key. Changes to the store stay in memory, never in the file. Empty " +
				"starts the store empty.",
			Set: func(s *settings, v config.Value) error { s.kvFile = v; return nil },
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
		config.Field[settings]{
			Key: "grpc.admin.dir", Kind: config.String,
			Usage: "Directory of the files that keep the mocks the gRPC server's admin API adds across restarts, which only " +
				"the server loads: keep it out of grpc.mock_files. Relative paths start at grpc.root_dir, and {workdir} at " +
				"workdir. Empty keeps them in memory.",
			Set: func(s *settings, v config.Value) error { s.grpcAdmin, s.grpcAdminDir = true, v.Str; return nil },
		},
	)
}

// loadSettings reads the command's settings from the sources of args, as the server reads its own.
func loadSettings(args []string) (settings, error) {
	s := defaultSettings()

	values, err := settingsSchema().LoadOnly(&s, mockhttp.DefaultConfigSources(args))

	var problems []config.Problem
	if err != nil {
		cfgErr, ok := errors.AsType[*config.Error](err)
		if !ok {
			return s, err
		}

		problems = cfgErr.Problems
	}

	problems = append(problems, s.checkAuth(values)...)

	loaded, tlsProblems := config.LoadTLS(values, "admin.tls.")
	s.certs = loaded.Certificates

	if problems = append(problems, tlsProblems...); len(problems) > 0 {
		return s, &config.Error{Problems: problems}
	}

	return s, nil
}

// checkAuth checks the credentials that values set: a secret set twice, a username without its password, and
// credentials the admin API refuses.
func (s settings) checkAuth(values map[string]config.Value) []config.Problem {
	var problems []config.Problem

	for _, key := range []string{"admin.auth.password", "admin.auth.token"} {
		if v, ok := values[key]; ok {
			if file, ok := values[key+"_file"]; ok {
				problems = append(problems, config.Problem{
					Code: config.CodeConflict, Origin: file.Origin, Key: file.Key,
					Message: fmt.Sprintf("%s and %s are both set, by %s", file.Key, key, v.Origin), Hint: "set one of them",
				})
			}
		}
	}

	if len(problems) > 0 {
		return problems
	}

	user, hasUser := values["admin.auth.username"]
	password, hasPassword := firstValue(values, "admin.auth.password", "admin.auth.password_file")

	switch {
	case hasUser && !hasPassword:
		return []config.Problem{{
			Code: config.CodeMissingValue, Origin: user.Origin, Key: "admin.auth.password",
			Message: "admin.auth.username is set but admin.auth.password is not", Hint: "set admin.auth.password or admin.auth.password_file",
		}}
	case hasPassword && !hasUser:
		return []config.Problem{{
			Code: config.CodeMissingValue, Origin: password.Origin, Key: "admin.auth.username",
			Message: password.Key + " is set but admin.auth.username is not", Hint: "set both",
		}}
	case hasUser:
		// Each is checked with a valid other, so a problem names the setting that has it.
		if _, err := admin.New(admin.BasicAuth(s.username, "-")); err != nil {
			problems = append(problems, config.Problem{
				Code: config.CodeInvalidValue, Origin: user.Origin, Key: user.Key, Message: err.Error(),
			})
		}

		if _, err := admin.New(admin.BasicAuth("-", s.password)); err != nil {
			problems = append(problems, config.Problem{
				Code: config.CodeInvalidValue, Origin: password.Origin, Key: password.Key, Message: err.Error(),
			})
		}
	}

	if token, ok := firstValue(values, "admin.auth.token", "admin.auth.token_file"); ok {
		if _, err := admin.New(admin.BearerToken(s.token)); err != nil {
			problems = append(problems, config.Problem{
				Code: config.CodeInvalidValue, Origin: token.Origin, Key: token.Key, Message: err.Error(),
			})
		}
	}

	return problems
}

// firstValue returns the value of the first of keys that values has.
func firstValue(values map[string]config.Value, keys ...string) (config.Value, bool) {
	for _, key := range keys {
		if v, ok := values[key]; ok {
			return v, true
		}
	}

	return config.Value{}, false
}

// readSecret reads the secret in file, without the line break that ends the file, which editors and echo add.
func readSecret(file string) (string, error) {
	b, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		return "", err
	}

	return strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r"), nil
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

// setupGRPC applies s to the gRPC server's setup. The gRPC server has no work directory: a grpc.admin.dir that starts
// with {workdir} starts at the HTTP server's, which httpConf configures.
func (s settings) setupGRPC(cb *mockgrpc.ConfigBuilder, httpConf *mockhttp.Config) (*mockgrpc.ConfigBuilder, error) {
	if !s.grpcAdmin {
		return cb, nil
	}

	cb.Admin(s.newAdmin(s.grpcAdminAddr))

	if s.grpcAdminDir == "" {
		return cb, nil
	}

	dir := s.grpcAdminDir

	// The path is absolute, so that grpc.root_dir doesn't apply to the HTTP server's work directory.
	if rest, ok := strings.CutPrefix(dir, mockhttp.WorkDirVar); ok && (rest == "" || os.IsPathSeparator(rest[0])) {
		var err error
		if dir, err = filepath.Abs(httpConf.Path(dir)); err != nil {
			return nil, fmt.Errorf("config: grpc.admin.dir: %w", err)
		}
	}

	return cb.AdminStore(mockgrpc.FileStore(dir)), nil
}

// newKV returns the key-value store of s: the CSV store of kv.file, or an empty memory store without one. The file
// loads here, as the server starts, and not with the settings: the health check loads them too, and mustn't fail for
// a file that changed since.
func (s settings) newKV() (vlatte.KV, error) {
	if s.kvFile.Str == "" {
		return vlatte.NewMemoryKV(), nil
	}

	kv, err := vlatte.NewCSVKV(s.kvFile.Str)
	if err != nil {
		return nil, &config.Error{Problems: config.ProblemsOf(s.kvFile, err)}
	}

	return kv, nil
}

// newAdmin returns an admin API on addr, which serves MCP too unless s turns it off.
func (s settings) newAdmin(addr string) *admin.Admin {
	options := []admin.Option{admin.Addr(addr), admin.DocsVersion(s.docsVersion)}
	if s.mcp {
		options = append(options, admin.MCP())
	}

	if s.username != "" {
		options = append(options, admin.BasicAuth(s.username, s.password))
	}

	if s.token != "" {
		options = append(options, admin.BearerToken(s.token))
	}

	if len(s.certs) > 0 {
		options = append(options, admin.TLS(&tls.Config{Certificates: s.certs, MinVersion: tls.VersionTLS12}))
	}

	return admin.MustNew(options...)
}
