package kcore_test

import (
	"context"
	"testing"

	"github.com/caffeine-projects/vlatte/kcore"
	"github.com/caffeine-projects/vlatte/khttp"
	. "github.com/caffeine-projects/vlatte/matcher"
)

func BenchmarkFindMockForRequest(b *testing.B) {
	ctx := context.Background()
	store := kcore.NewStore[*kcore.BaseMock]()
	desc := &kcore.Results{}
	expectations := []*kcore.Expectation[string]{
		{Matcher: Function(func(v any) (bool, error) { return true, nil })},
		{Matcher: Function(func(v any) (bool, error) { return true, nil })},
		{Matcher: Function(func(v any) (bool, error) { return true, nil })},
		{Matcher: Function(func(v any) (bool, error) { return true, nil })},
	}
	fnExp := func(bm *kcore.BaseMock) []*kcore.Expectation[string] { return expectations }

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		kcore.FindMockForRequest[string, *kcore.BaseMock](
			ctx,
			store,
			fnExp,
			"test",
			desc,
			kcore.FindOptions{},
		)
	}
}

func BenchmarkMatch(b *testing.B) {
	params := &khttp.HTTPValueSelectorInput{}
	desc := &kcore.Results{}
	matcher := Function(func(_ any) (bool, error) {
		return true, nil
	})

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pass, _ := kcore.Match(context.Background(), params, desc, []*kcore.Expectation[*khttp.HTTPValueSelectorInput]{{
			Matcher: matcher,
			ValueSelector: func(_ context.Context, r *khttp.HTTPValueSelectorInput) any {
				return "dev"
			},
		}}, kcore.FindOptions{})

		if !pass {
			b.FailNow()
		}
	}
}
