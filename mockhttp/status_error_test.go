package mockhttp

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStatusError_IsErrStatusWhereverItIsWrapped(t *testing.T) {
	// The server finds the status error inside the error matching returns, which wraps it with the mock it came from.
	cause := errors.New("name: required")
	err := fmt.Errorf("mock: matching: %w", &StatusError{Status: 400, Err: cause})

	require.ErrorIs(t, err, ErrStatus)
	require.ErrorIs(t, err, cause)

	se, ok := errors.AsType[*StatusError](err)
	require.True(t, ok)
	require.Equal(t, 400, se.Status)
}

func TestStatusError_MessageNamesTheStatusAndWhy(t *testing.T) {
	require.Equal(t, "http: status 422", (&StatusError{Status: 422}).Error())
	require.Equal(t, "http: status 400: name: required", (&StatusError{Status: 400, Err: errors.New("name: required")}).Error())
}
