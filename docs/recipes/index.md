# Recipes

A recipe prepares a server, for example by adding a set of mocks. `m.Prep(ctx, recipes...)` runs recipes in order, and stops at the first that fails. Recipes live under `recipe/<protocol>/<recipe>`.

```go
m := mockhttp.MustNew()
err := m.Prep(ctx, proxyall.New(mockhttp.From("https://api.example.com").Timeout(5*time.Second)))
```

| Recipe | Prepares |
|---|---|
| [Proxy all](/recipes/proxy-all) | A mock that sends what no other mock matches to a real service. |

A recipe of your own takes a few lines: see [Writing a recipe](/recipes/writing).
