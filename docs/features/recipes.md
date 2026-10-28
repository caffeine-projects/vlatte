# Recipes

A recipe prepares a server, for example by adding a set of mocks. `m.Prep(ctx, recipes...)` runs recipes in order, and stops at the first that fails. Recipes live under `recipe/<protocol>/<recipe>`.

```go
m := mockhttp.MustNew()
err := m.Prep(ctx, proxyall.New(mockhttp.From("https://api.example.com").Timeout(5*time.Second)))
```

| Recipe | Prepares |
|---|---|
| `recipe/http/proxyall` | A [proxy all](/http/proxying#proxy-all) mock: what no other mock matches goes to the target, with the options of the proxy reply it takes. |

## Writing a recipe

A recipe is a `vlatte.Recipe`: its `Prep(ctx, input)` gets the server in `input.Server`, and adds mocks or changes what it serves. `mockhttp.Recipe` and `mockgrpc.Recipe` name the recipes of each protocol.

```go
type users struct{}

func (users) Prep(_ context.Context, in *vlatte.RecipeInput[*mockhttp.Mock, *mockhttp.Server]) error {
	_, err := in.Server.Mock(
		mockhttp.Getf("/users").Reply(mockhttp.OK().JSON([]any{})),
		mockhttp.Postf("/users").Reply(mockhttp.Created()),
	)

	return err
}
```
