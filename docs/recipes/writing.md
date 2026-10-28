# Writing a recipe

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

Run it as you run the built-in ones: `m.Prep(ctx, users{})`.
