package xhttp

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/caffeine-projects/vlatte/filter"
)

func TestRace(t *testing.T) {
	m := NewMock()
	jobs := 10
	wg := sync.WaitGroup{}

	for i := 0; i < jobs; i++ {
		wg.Add(1)
		go func() {
			m.Inc()
			m.Hits()
			wg.Done()
		}()

		m.Inc()
	}

	m.Hits()
	m.Inc()
	m.Inc()

	wg.Wait()

	assert.EqualValues(t, (jobs*2)+2, m.Hits())
}

func TestMock(t *testing.T) {
	m := NewMock()

	t.Run("should init enabled", func(t *testing.T) {
		assert.True(t, m.Roasted())
	})

	t.Run("should disable mock when calling .Raw()", func(t *testing.T) {
		m.Raw()
		assert.False(t, m.Roasted())

		m.Roast()
		assert.True(t, m.Roasted())
	})

	t.Run("should return called when it was hit", func(t *testing.T) {
		assert.False(t, m.Called())
		m.Inc()
		assert.True(t, m.Called())

		m.Dec()
		assert.False(t, m.Called())
	})
}

func TestMock_MarshalJSON(t *testing.T) {
	app := New()
	builder := Request().
		Header("accept", Eq("test")).
		Once().
		ScenarioIs("test").ScenarioStateIs("state").ScenarioStateWillBe("future").
		Reply(OK())

	m, err := builder.Roast(app)
	require.NoError(t, err)

	b, err := m.MarshalJSON()
	require.NoError(t, err)

	fmt.Println(string(b))
}
