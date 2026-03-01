package vlatte

import "github.com/rs/zerolog"

// LogVerbosity defines the verbosity of the logs.
type LogVerbosity int8

const (
	LogBasic LogVerbosity = iota
	LogHeader
	LogBody
)

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

const (
	LogLevelDebug    = LogLevel(zerolog.DebugLevel)
	LogLevelInfo     = LogLevel(zerolog.InfoLevel)
	LogLevelWarn     = LogLevel(zerolog.WarnLevel)
	LogLevelError    = LogLevel(zerolog.ErrorLevel)
	LogLevelNone     = LogLevel(zerolog.NoLevel)
	LogLevelDisabled = LogLevel(zerolog.Disabled)
)
