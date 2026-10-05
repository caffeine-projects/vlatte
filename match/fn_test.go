package match

// func TestFn(t *testing.T) {
// 	m := Function(func(v any) (bool, error) {
// 		return v.(string) == "ok", nil
// 	})

// 	t.Run("pass", func(t *testing.T) {
// 		ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "ok"))
// 		require.NoError(t, err)
// 		require.True(t, ok)
// 	})

// 	t.Run("no pass", func(t *testing.T) {
// 		ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "nok"))
// 		require.NoError(t, err)
// 		require.False(t, ok)
// 	})
// }

// func TestFnError(t *testing.T) {
// 	ok, err := Function(
// 		func(_ any) (bool, error) {
// 			return false, errors.New("boom")
// 		}).
// 		Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "ok"))

// 	require.Error(t, err)
// 	require.False(t, ok)
// }
