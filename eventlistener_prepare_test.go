package vlatte

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// preparer is an event listener that prepares its parameters with prepare.
type preparer struct {
	prepare func(cfg EventConfig, engine TemplateEngine) (Parameters, error)
}

func (p preparer) Run(context.Context, OnComplete) error { return nil }

func (p preparer) Prepare(cfg EventConfig, engine TemplateEngine) (Parameters, error) {
	return p.prepare(cfg, engine)
}

func TestEventRegistry_Prepare(t *testing.T) {
	engine := MustNewGoTemplateEngine()
	cfg := EventConfig{Name: "hook", Parameters: map[string]any{"url": "x"}, Dir: "mocks"}

	t.Run("a preparer gets the config and the engine, and its parameters are the event's", func(t *testing.T) {
		r := NewEventRegistry[OnComplete]()
		require.NoError(t, r.Register("hook", preparer{prepare: func(got EventConfig, e TemplateEngine) (Parameters, error) {
			require.Equal(t, cfg, got)
			require.Same(t, engine, e)

			return "prepared", nil
		}}))

		params, err := r.Prepare(cfg, engine)
		require.NoError(t, err)
		require.Equal(t, "prepared", params)
	})

	t.Run("a preparer's error is returned", func(t *testing.T) {
		r := NewEventRegistry[OnComplete]()
		require.NoError(t, r.Register("hook", preparer{prepare: func(EventConfig, TemplateEngine) (Parameters, error) {
			return nil, errors.New("hook: bad url")
		}}))

		_, err := r.Prepare(cfg, engine)
		require.EqualError(t, err, "hook: bad url")
	})

	t.Run("other listeners and unknown names keep the parameters", func(t *testing.T) {
		r := NewEventRegistry[OnComplete]()
		require.NoError(t, r.Register("hook", EventFunc[OnComplete](func(context.Context, OnComplete) error { return nil })))

		for _, name := range []string{"hook", "nobody"} {
			params, err := r.Prepare(EventConfig{Name: name, Parameters: cfg.Parameters}, engine)
			require.NoError(t, err)
			require.Equal(t, cfg.Parameters, params)
		}
	})
}
