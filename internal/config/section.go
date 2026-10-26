package config

import "github.com/rs/zerolog"

// LogLevels are the choices of a log level setting, the levels of zerolog.
var LogLevels = []Choice{
	{Name: "trace", Value: int64(zerolog.TraceLevel)},
	{Name: "debug", Value: int64(zerolog.DebugLevel)},
	{Name: "info", Value: int64(zerolog.InfoLevel)},
	{Name: "warn", Value: int64(zerolog.WarnLevel)},
	{Name: "error", Value: int64(zerolog.ErrorLevel)},
	{Name: "fatal", Value: int64(zerolog.FatalLevel)},
	{Name: "panic", Value: int64(zerolog.PanicLevel)},
	{Name: "none", Value: int64(zerolog.NoLevel)},
	{Name: "disabled", Value: int64(zerolog.Disabled)},
}

// Section returns the section *p points to, turning it on with def when it is off: the Set of a key of a section
// calls it, as setting a key turns its section on.
func Section[S any](p **S, def S) *S {
	if *p == nil {
		*p = &def
	}

	return *p
}

// Toggle turns a section on, keeping its settings if it already is, or off: the Set of the section's own key.
func Toggle[S any](p **S, def S, on bool) {
	if !on {
		*p = nil
		return
	}

	Section(p, def)
}
