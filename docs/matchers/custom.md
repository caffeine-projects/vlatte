# Custom matchers

Create matchers from Go functions, or register named matchers to use in mock files, admin API requests and journal queries.

## In Go

Any `vlatte.Matcher` works where a matcher goes. `match.Function(fn)` and `match.Predicate(fn)` make one from a function, and a mock's whole-request matchers get the request itself:

| Protocol | Method | Gets |
|---|---|---|
| HTTP | `Matches(m)`, `RequestMatches(fn)` | The `*http.Request`. |
| gRPC | `Matches(m)`, `CallMatches(fn)` | The `*mockgrpc.Call`. |

```go
m.MustMock(mockhttp.Postf("/upload").
	RequestMatches(func(r *http.Request) (bool, error) { return r.ContentLength > 2048, nil }).
	Reply(mockhttp.Status(http.StatusRequestEntityTooLarge)))
```

A predicate, such as `RequestMatches` or `CallMatches`, can't be described: the admin API can't show, save or store its mock. A matcher given to `Matches` that describes itself is described, under `request.custom`.

## In mock files

A server reads mock files with its registry of matchers: the built-in ones, and the custom ones you give it. A custom matcher is built from its arguments when the mock loads, and its name works anywhere a matcher list does, in mock files, admin API mocks and request journal queries. `request.custom` lists matchers of the whole request: each gets the `*http.Request` (`Builder.Matches` in Go). A registry belongs to the servers you give it to; servers can share one, gRPC ones too, whose `request.custom` matchers get the `*mockgrpc.Call`.

```go
registry := matchbuild.MustNew(matchbuild.WithMatcher("body-too-long",
	func(_ *matchbuild.Registry, args []any) (vlatte.Matcher, error) {
		if len(args) != 1 {
			return nil, errors.New("takes a length")
		}

		limit, ok := args[0].(int64)
		if !ok {
			return nil, errors.New("takes a length")
		}

		return match.Function(func(v any) (bool, error) {
			r, ok := v.(*http.Request)
			return ok && r.ContentLength > limit, nil
		}), nil
	}))

m := mockhttp.MustNew(mockhttp.Setup().Matchers(registry))
```

```yaml
request:
  custom: [[body-too-long, 2048]]
response:
  status: 413
```

`matchbuild.Registry` builds the matchers of mock files: `registry.BuildMatcher(spec)` builds one from its spec in Go, as a mock file does.
