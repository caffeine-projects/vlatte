package vlatte

// Configurer lets users configure the Mock API.
type Configurer[TConfig any] interface {
	// Apply applies a configuration.
	Apply(conf TConfig) error
}
