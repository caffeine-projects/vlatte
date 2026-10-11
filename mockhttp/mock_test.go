package mockhttp

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/match"
)

func TestRace(t *testing.T) {
	m := &Mock{}
	jobs := 10
	wg := sync.WaitGroup{}

	for range jobs {
		wg.Go(func() {
			m.Inc()
			m.Hits()
		})

		m.Inc()
	}

	m.Hits()
	m.Inc()
	m.Inc()

	wg.Wait()

	assert.EqualValues(t, (jobs*2)+2, m.Hits())
}

func TestMock(t *testing.T) {
	m, err := Getf("/mock").Reply(OK()).Build(MustNew(Setup().Silent()))
	require.NoError(t, err)

	t.Run("should init enabled", func(t *testing.T) {
		assert.True(t, m.Enabled())
	})

	t.Run("should disable mock when calling .Disable()", func(t *testing.T) {
		m.Disable()
		assert.False(t, m.Enabled())

		m.Enable()
		assert.True(t, m.Enabled())
	})

	t.Run("should return called when it was hit", func(t *testing.T) {
		assert.False(t, m.Called())
		m.Inc()
		assert.True(t, m.Called())
	})
}

func TestMock_MarshalJSON(t *testing.T) {
	app := MustNew()
	builder := Request().
		Header("accept", match.Eq("test")).
		Once().
		ScenarioIs("test").ScenarioStateIs("state").ScenarioStateWillBe("future").
		Reply(OK())

	m, err := builder.Build(app)
	require.NoError(t, err)

	b, err := m.MarshalJSON()
	require.NoError(t, err)

	fmt.Println(string(b))
}

func TestMock_String(t *testing.T) {
	// Failure bodies and matching errors name a mock with String, so it must read the same as assertion messages.
	m := MustNew(Setup().Silent())

	named, err := Getf("/").ID("a").Name("users").Reply(OK()).Build(m)
	require.NoError(t, err)
	require.Equal(t, "Mock [a] users", named.String())

	unnamed, err := Getf("/").ID("b").Reply(OK()).Build(m)
	require.NoError(t, err)
	require.Equal(t, "Mock [b]", unnamed.String())
}
