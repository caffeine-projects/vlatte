package vlatte

// func TestCanister(t *testing.T) {
// 	st := NewCanister[*GreenBean]()
// 	ctx := context.Background()

// 	mock1 := &GreenBean{ID: "1", Name: "mock_1", enabled: true, Priority: 0}
// 	mock2 := &GreenBean{ID: "2", Name: "mock_2", enabled: true, Priority: 1}
// 	mock3 := &GreenBean{ID: "3", Name: "mock_3", enabled: true, Priority: 2}

// 	err := st.Store(ctx, mock1)
// 	require.NoError(t, err)

// 	err = st.Store(ctx, mock2)
// 	require.NoError(t, err)

// 	err = st.Store(ctx, mock3)
// 	require.NoError(t, err)

// 	mock, err := st.Pick(ctx, mock1.ID)
// 	require.NoError(t, err)
// 	require.Equal(t, mock1, mock)

// 	mocks, err := st.PickAll(ctx)
// 	require.NoError(t, err)

// 	m := mocks[0]
// 	require.Equal(t, m.ID, "1")
// 	require.Equal(t, m.Name, "mock_1")

// 	m.Raw()

// 	eligible := make([]*GreenBean, 0)
// 	done := make(chan struct{})
// 	out, err := st.ScoopCuratedStream(ctx, done)
// 	for v := range out {
// 		eligible = append(eligible, v)
// 	}

// 	require.NoError(t, err)
// 	require.Len(t, eligible, 2)

// 	eligible, err = st.PickAll(ctx)
// 	require.NoError(t, err)
// 	require.Len(t, eligible, 3)

// 	err = st.Toss(ctx, "2")
// 	require.NoError(t, err)
// 	eligible, err = st.PickAll(ctx)
// 	require.NoError(t, err)
// 	require.Len(t, eligible, 2)

// 	eligible = make([]*GreenBean, 0)
// 	out, err = st.ScoopCuratedStream(ctx, done)
// 	for v := range out {
// 		eligible = append(eligible, v)
// 	}

// 	require.NoError(t, err)
// 	require.Len(t, eligible, 1)
// 	require.Equal(t, "3", eligible[0].ID)

// 	err = st.Empty(ctx)
// 	require.NoError(t, err)

// 	eligible, err = st.PickAll(ctx)
// 	require.NoError(t, err)
// 	require.Len(t, eligible, 0)

// 	err = st.Store(ctx, &GreenBean{ID: "10", Name: "mock_ext_1", enabled: true, Priority: 0, Source: "ext"})
// 	require.NoError(t, err)
// 	err = st.Store(ctx, &GreenBean{ID: "11", Name: "mock_11", enabled: true, Priority: 0})
// 	require.NoError(t, err)

// 	all, err := st.PickAll(ctx)
// 	require.NoError(t, err)
// 	require.Len(t, all, 2)

// 	err = st.Empty(ctx)
// 	require.NoError(t, err)

// 	all, err = st.PickAll(ctx)
// 	require.NoError(t, err)
// 	require.Len(t, all, 0)
// }
