package sse

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/config"
)

func TestDoc_KeepsJSONNumbersAFileCantHold(t *testing.T) {
	// A number past int64, as a uint64 id, would round once a file reads it back: it is written as its JSON text, which
	// sends the same bytes.
	s := &Spec{Events: []*EventSpec{
		{Data: []byte(`18446744073709551615`), DataKind: DataJSON, HasData: true},
		{Data: []byte(`{"id":18446744073709551615}`), DataKind: DataJSON, HasData: true},
	}}

	text, err := config.Encode(s.Doc(), "yaml")
	require.NoError(t, err)

	doc, err := config.Parse(text, "yaml")
	require.NoError(t, err)

	back, err := Decode(config.Value{Key: "event_stream", Any: doc})
	require.NoError(t, err)
	require.Equal(t, wire(t, s), wire(t, back), "%s", text)
}
