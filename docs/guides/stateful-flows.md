# Stateful flows

This guide mocks a cart and checkout flow, where an answer depends on what came before, and runs its tests in parallel against one server. It uses [scenarios](/scenarios/), scenario keys, [priorities](/more/mock-lifecycle) and [templates](/templating/).

## The flow

A scenario starts in `started`. Adding an item moves it to `has-items`; checking out requires `has-items` and moves it to `paid`. A checkout in any other state falls through to the mock of priority 1, which refuses it. The order's status reads the scenario's state in a template.

`ScenarioKey` gives each value of `X-Test-Id` an instance of the scenario of its own, so tests that run at once each move their own. A key that renders empty plays in the shared instance.

## The tests

The request helper sends each subtest's name as its `X-Test-Id`. The parent test creates one server and registers the flow before its subtests run in parallel:

```go
func call(t *testing.T, m *mockhttp.Server, method, path string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, m.URL(path), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Test-Id", t.Name())

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}

	return res.StatusCode, strings.TrimSpace(string(body))
}

func TestCheckout(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	const key = `{{ .Request.Header.Get "X-Test-Id" }}`

	m.MustMock(
		mockhttp.Postf("/cart/items").
			StartScenario("checkout").ScenarioKey(key).ScenarioStateWillBe("has-items").
			Reply(mockhttp.Created()),
		mockhttp.Postf("/checkout").
			ScenarioIs("checkout").ScenarioKey(key).ScenarioStateIs("has-items").ScenarioStateWillBe("paid").
			Reply(mockhttp.OK().JSON(map[string]any{"order": 1})),
		mockhttp.Postf("/checkout").Priority(1).
			Reply(mockhttp.Status(http.StatusConflict).PlainText("the cart is empty")),
		mockhttp.Getf("/orders/1").
			Reply(mockhttp.OK().BodyTemplate(`{"status": "{{ .Scenarios.StateOf "checkout" (.Request.Header.Get "X-Test-Id") }}"}`)),
	)

	t.Run("pays a cart with items", func(t *testing.T) {
		t.Parallel()

		call(t, m, http.MethodPost, "/cart/items")
		if status, _ := call(t, m, http.MethodPost, "/checkout"); status != http.StatusOK {
			t.Fatalf("checkout = %d", status)
		}
		if _, body := call(t, m, http.MethodGet, "/orders/1"); body != `{"status": "paid"}` {
			t.Errorf("order = %s", body)
		}
	})

	t.Run("refuses an empty cart", func(t *testing.T) {
		t.Parallel()

		if status, _ := call(t, m, http.MethodPost, "/checkout"); status != http.StatusConflict {
			t.Fatalf("checkout = %d", status)
		}
		if _, body := call(t, m, http.MethodGet, "/orders/1"); body != `{"status": "started"}` {
			t.Errorf("order = %s", body)
		}
	})
}
```

## Watching it move

The admin API's `/scenario-board` page shows where each scenario and each of its keys is, and moves them with a click. In Go, `m.Scenarios(ctx)` lists them, `m.SetScenario(ctx, "checkout", "has-items", matchscenario.Key("t1"))` moves one, and `m.ResetScenarios()` starts every one over.

## The same in mock files

```yaml
id: checkout
scenario:
  name: checkout
  required_state: has-items
  new_state: paid
  key: '{{ .Request.Header.Get "X-Test-Id" }}'
request:
  method: POST
  path: /checkout
response:
  status: 200
  body:
    order: 1
```

## What you used

- `StartScenario`, `ScenarioIs`, `ScenarioStateIs`, `ScenarioStateWillBe`, `ScenarioKey`: [Scenarios](/scenarios/).
- `Priority`: [Mock lifecycle](/more/mock-lifecycle).
- `BodyTemplate`, `.Scenarios.StateOf`: [Templates](/templating/).
