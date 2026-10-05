package mockgrpc

import "github.com/caffeine-projects/vlatte"

// Names for the core types gRPC mock users write, so tests need only this package.
type (
	// Scope holds the mocks added by one Mock call; see [vlatte.Scope].
	Scope = vlatte.Scope[*Mock]

	// Option configures a Server. Both Config and ConfigBuilder are options.
	Option = vlatte.Configurer[*Config]

	// Recipe prepares a Server, for example by adding a set of mocks; see [Server.Prep].
	Recipe = vlatte.Recipe[*Mock, *Server]
)
