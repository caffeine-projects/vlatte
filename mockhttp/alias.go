package mockhttp

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
)

// Names for the core types HTTP mock users write, so tests need only this package.
type (
	// ConfigSource is a place server settings are read from: see ConfigFile, ConfigEnv, ConfigArgs
	// and [ConfigBuilder.ConfigSources].
	ConfigSource = config.Source

	// ConfigFlag describes the command-line flag of a server setting; see ConfigFlags.
	ConfigFlag = config.Flag

	// Scope holds the mocks added by one Mock call; see [vlatte.Scope].
	Scope = vlatte.Scope[*Mock]

	// Option configures a Server. Both Config and ConfigBuilder are options.
	Option = vlatte.Configurer[*Config]

	// Replier builds the response of a mock. Reply, EchoReply and ProxyReply are repliers.
	Replier = vlatte.Replier[ReplyInput, *Response]

	// Recipe prepares a Server, for example by adding a set of mocks; see [Server.Prep].
	Recipe = vlatte.Recipe[*Mock, *Server]
)
