# Scenarios

A scenario serves mocks in turn: each mock of a scenario matches while the scenario is in the state it requires, and moves the scenario to its new state when it is picked to answer. Every scenario starts in `started`.

::: code-group

```go [HTTP]
m.MustMock(
	mockhttp.Postf("/orders").ID("create").
		StartScenario("order").ScenarioStateWillBe("created").
		Reply(mockhttp.Created()),
	mockhttp.Getf("/orders/1").ID("read").
		ScenarioIs("order").ScenarioStateIs("created").
		Reply(mockhttp.OK().JSON(map[string]any{"id": 1})),
)
```

```go [gRPC]
m.MustMock(
	mockgrpc.Method("orders.Orders/Create").ID("create").
		StartScenario("order").ScenarioStateWillBe("created").
		Reply(mockgrpc.JSON(`{"id": "1"}`)),
	mockgrpc.Method("orders.Orders/Get").ID("read").
		ScenarioIs("order").ScenarioStateIs("created").
		Reply(mockgrpc.JSON(`{"id": "1", "status": "CREATED"}`)),
)
```

```yaml [Mock file]
id: read
scenario: {name: order, required_state: created, new_state: read}
request: {method: GET, path: /orders/1}
response: {status: 200, body: '{"id": 1}'}
```

:::

gRPC mocks take part in scenarios the same way, and their mock files take the same `scenario` key.

## States

A mock that requires no state matches in any state. A mock that requires a state no request moved the scenario to yet doesn't match: a typo in a scenario's name never matches every request.

## Turns

The scenario moves before the mock answers, so the next request matches in the new state, and of two concurrent requests in one state only one gets the mock of that state. It moves even when the mock then doesn't answer, such as when its client leaves during the mock's `delay`.

## Names

Names ignore case: `Checkout` is `checkout`. A scenario is listed under the name its first mock gives it.

## Keys

A mock with a scenario key plays in an instance of the scenario for each key: tests that run at once against one server, or tenants of one API, each move their own. The key is a template that renders with the request, as reply templates do: ``ScenarioKey(`{{ .Request.Header.Get "X-Test-Id" }}`)`` in Go, `key` under `scenario` in a mock file. Each key starts in `started`; keys are exact, so `t1` is not `T1`. A key that renders empty plays in the shared instance, the one of mocks without a key, and a key that fails to render fails the request's matching.

```yaml
scenario: {name: checkout, required_state: started, new_state: paid, key: '{{ .Request.Header.Get "X-Test-Id" }}'}
```

## Moving scenarios

In Go, `m.Scenarios(ctx)` lists each scenario with its state, the states of its keyed instances that moved, the states its mocks name, flagging those no path of mock moves reaches from `started`, and which mock moves it where. `m.SetScenario(ctx, name, state)` moves a scenario, and refuses a state none of its mocks names with `matchscenario.ErrUnknownState`; a scenario no mock names takes any state. `m.ResetScenario(name)` starts every instance of a scenario over, and `m.ResetScenarios()` every scenario. `matchscenario.Key("t1")`, as the last argument of `SetScenario` and `ResetScenario`, picks the instance of a key.

In the [admin API](/admin-api/), `GET /scenarios`, `PUT /scenarios/{name}`, `DELETE /scenarios/{name}` and `DELETE /scenarios` do the same, and `?key=t1` picks the instance of a key. Its `/scenario-board` page does it from a browser: it shows where each scenario and each of its keys is, flags the states no path of mock moves reaches from `started`, moves a scenario with a click, and follows the moves as requests make them.

## In templates, near misses and the journal

- A request no mock matched names the scenario a mock waits on: `differs in scenario order needs created, is started`.
- Templates read a scenario's state, the one its mock moved it to: `{{ .Scenarios.State "checkout" }}`, and the state of a key: `{{ .Scenarios.StateOf "checkout" (.Request.Header.Get "X-Test-Id") }}`. One mock can then answer in every state.
- The [journal](/more/journal) keeps the moves of each request's mock under `scenarios`, and [`GET /events`](/admin-api/) streams a `scenario` event for each move, by a request or by the methods and admin API above, in the order the moves happened.
