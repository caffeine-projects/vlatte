package kvview_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/kvview"
)

var errKVDown = errors.New("kv down")

// racedKV is a key-value store where every key is removed between Has and Get, as another client of a shared store can
// do.
type racedKV struct {
	vlatte.KV
}

func (racedKV) Has(context.Context, string) (bool, error) { return true, nil }

func (racedKV) Get(_ context.Context, k string) ([]byte, error) {
	return nil, fmt.Errorf("%w: %q", vlatte.ErrKVKeyNotFound, k)
}

// downKV is a key-value store that can't answer.
type downKV struct {
	vlatte.KV
}

func (downKV) Has(context.Context, string) (bool, error) { return false, errKVDown }

func (downKV) Get(context.Context, string) ([]byte, error) { return nil, errKVDown }

func TestView_AKeyGoneFromTheStoreIsAMissingKey(t *testing.T) {
	// A template reads a key the store no longer holds: it fails as for a key never set, not with the store's error,
	// and never renders an empty value as if the key were set to nothing.
	view := kvview.New(t.Context(), racedKV{})

	_, err := view.Get("name")
	require.EqualError(t, err, `key "name" is not set: set it, or check for it with .KV.Has`)
}

func TestView_AStoreErrorNamesTheKey(t *testing.T) {
	// A remote store fails for one key among the many a template reads: the error says which.
	view := kvview.New(t.Context(), downKV{})

	_, err := view.Get("name")
	require.ErrorIs(t, err, errKVDown)
	require.EqualError(t, err, `reading key "name": kv down`)

	_, err = view.Has("name")
	require.ErrorIs(t, err, errKVDown)
	require.EqualError(t, err, `reading key "name": kv down`)
}
