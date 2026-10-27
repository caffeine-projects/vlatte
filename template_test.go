package vlatte

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// provider is a TemplateDataProvider of a string input that gives what its function returns.
type provider struct {
	key  string
	data func(in string) (any, error)
}

func (p provider) Key() string { return p.key }

func (p provider) Data(in string) (any, error) { return p.data(in) }

func TestProvideTemplateData_GivesEachProvidersDataByItsKey(t *testing.T) {
	providers := []TemplateDataProvider[string]{
		provider{key: "echo", data: func(in string) (any, error) { return in, nil }},
		provider{key: "n", data: func(string) (any, error) { return 1, nil }},
	}

	data, err := ProvideTemplateData(providers, "call")
	require.NoError(t, err)
	require.Equal(t, map[string]any{"echo": "call", "n": 1}, data)
}

func TestProvideTemplateData_AProviderThatFailsNamesItself(t *testing.T) {
	// The server answers with the error: it says which provider failed, as a server can have several.
	errDown := errors.New("user service down")
	providers := []TemplateDataProvider[string]{
		provider{key: "user", data: func(string) (any, error) { return nil, errDown }},
	}

	_, err := ProvideTemplateData(providers, "call")
	require.ErrorIs(t, err, errDown)
	require.EqualError(t, err, "template data provider user: user service down")
}

func TestCheckTemplateDataProviders_RefusesTwoOfOneKey(t *testing.T) {
	// Templates could read only one of them: a setup mistake, which the server fails to start with.
	a := provider{key: "a", data: func(string) (any, error) { return nil, nil }}
	b := provider{key: "b", data: func(string) (any, error) { return nil, nil }}

	require.NoError(t, CheckTemplateDataProviders([]TemplateDataProvider[string]{a, b}))

	err := CheckTemplateDataProviders([]TemplateDataProvider[string]{a, b, a})
	require.ErrorIs(t, err, ErrTemplateDataKeyExists)
	require.ErrorContains(t, err, `"a"`)
}
