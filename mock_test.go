package vlatte

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanister(t *testing.T) {
	st := NewJar[*M]()
	ctx := context.Background()

	mock1 := &M{ID: "1", Name: "mock_1", enabled: true, Priority: 0}
	mock2 := &M{ID: "2", Name: "mock_2", enabled: true, Priority: 1}
	mock3 := &M{ID: "3", Name: "mock_3", enabled: true, Priority: 2}

	err := st.Save(ctx, mock1)
	require.NoError(t, err)

	err = st.Save(ctx, mock2)
	require.NoError(t, err)

	err = st.Save(ctx, mock3)
	require.NoError(t, err)

	mock, err := st.Get(ctx, mock1.ID)
	require.NoError(t, err)
	require.Equal(t, mock1, mock)

	mocks, err := st.GetAll(ctx)
	require.NoError(t, err)

	m := mocks[0]
	require.Equal(t, m.ID, "1")
	require.Equal(t, m.Name, "mock_1")

	m.Disable()

	eligible := make([]*M, 0)
	done := make(chan struct{})
	out, err := st.EligibleStream(ctx, done)
	for v := range out {
		eligible = append(eligible, v)
	}

	require.NoError(t, err)
	require.Len(t, eligible, 2)

	eligible, err = st.GetAll(ctx)
	require.NoError(t, err)
	require.Len(t, eligible, 3)

	err = st.Del(ctx, "2")
	require.NoError(t, err)
	eligible, err = st.GetAll(ctx)
	require.NoError(t, err)
	require.Len(t, eligible, 2)

	eligible = make([]*M, 0)
	out, err = st.EligibleStream(ctx, done)
	for v := range out {
		eligible = append(eligible, v)
	}

	require.NoError(t, err)
	require.Len(t, eligible, 1)
	require.Equal(t, "3", eligible[0].ID)

	err = st.DelAll(ctx)
	require.NoError(t, err)

	eligible, err = st.GetAll(ctx)
	require.NoError(t, err)
	require.Len(t, eligible, 0)

	err = st.Save(ctx, &M{ID: "10", Name: "mock_ext_1", enabled: true, Priority: 0, Source: "ext"})
	require.NoError(t, err)
	err = st.Save(ctx, &M{ID: "11", Name: "mock_11", enabled: true, Priority: 0})
	require.NoError(t, err)

	all, err := st.GetAll(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)

	err = st.DelAll(ctx)
	require.NoError(t, err)

	all, err = st.GetAll(ctx)
	require.NoError(t, err)
	require.Len(t, all, 0)
}
