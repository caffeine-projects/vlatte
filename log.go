package vlatte

import "github.com/rs/zerolog"

// LogVerbosity defines the verbosity of the logs.
type LogVerbosity int8

// Log verbosities, each logging what the one before it does, and more.
const (
	// LogBasic logs the requests and their answers without their headers or bodies.
	LogBasic LogVerbosity = iota
	// LogHeader adds the headers, with those the server redacts redacted.
	LogHeader
	// LogBody adds the bodies.
	LogBody
)

// String implements [fmt.Stringer]. It returns "none" for a value that is not a verbosity.
func (l LogVerbosity) String() string {
	switch l {
	case LogBasic:
		return "basic"
	case LogHeader:
		return "header"
	case LogBody:
		return "body"
	default:
		return "none"
	}
}

// LogLevel sets the level of the default logger.
type LogLevel = int8

// Log levels. The default logger writes the entries at its level and above.
const (
	LogLevelDebug = LogLevel(zerolog.DebugLevel)
	LogLevelInfo  = LogLevel(zerolog.InfoLevel)
	LogLevelWarn  = LogLevel(zerolog.WarnLevel)
	LogLevelError = LogLevel(zerolog.ErrorLevel)
	// LogLevelNone writes only the entries that have no level.
	LogLevelNone = LogLevel(zerolog.NoLevel)
	// LogLevelDisabled writes nothing.
	LogLevelDisabled = LogLevel(zerolog.Disabled)
)
