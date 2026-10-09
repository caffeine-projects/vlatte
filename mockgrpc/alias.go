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

	// UnaryReplier serves the calls of one unary mock. Each mock has its own, made by a UnaryReplierFactory.
	UnaryReplier = vlatte.Replier[*UnaryRequestValues, any]

	// UnaryReplierFactory makes the UnaryReplier of each mock. Unary is one.
	UnaryReplierFactory = vlatte.ReplierFactory[*UnaryRequestValues, any, *Server]

	// StreamReplier serves the calls of one server streaming mock. Each mock has its own, made by a
	// StreamReplierFactory.
	StreamReplier = vlatte.Replier[*StreamRequestValues, any]

	// StreamReplierFactory makes the StreamReplier of each mock. ServerStream is one.
	StreamReplierFactory = vlatte.ReplierFactory[*StreamRequestValues, any, *Server]
)
