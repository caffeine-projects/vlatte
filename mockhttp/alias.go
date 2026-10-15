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

	// Replier builds the response of a mock. Each mock has its own, made by a ReplierFactory.
	Replier = vlatte.Replier[ReplyInput, *Response]

	// ReplierFactory makes the Replier of each mock: see [vlatte.ReplierFactory]. Reply, SequentialReply, RandomReply,
	// ProxyReply, FuncReply and EchoReply are factories, and Shared turns any Replier into one.
	ReplierFactory = vlatte.ReplierFactory[ReplyInput, *Response, *Server]

	// Recipe prepares a Server, for example by adding a set of mocks; see [Server.Prep].
	Recipe = vlatte.Recipe[*Mock, *Server]

	// RecordedMock is a mock a recording made, as RecordConfig.Transforms change it; see [vlatte.RecordedMock].
	RecordedMock = vlatte.RecordedMock[RecordedRequest, RecordedResponse]
)
