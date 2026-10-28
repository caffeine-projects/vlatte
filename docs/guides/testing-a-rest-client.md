# Testing a REST client

This guide tests a small Go client of a users API against a mock server: the happy path, an error answer, and what the client sent. It uses [mocks](/http/mocks), [request matching](/http/matching), [replies](/http/replies) and the [request journal](/features/journal).

## The client

```go
type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Client struct {
	BaseURL string
	Token   string
}

func (c *Client) Get(ctx context.Context, id int) (*User, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/users/%d", c.BaseURL, id), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("users: get %d: %s", id, res.Status)
	}

	var u User
	return &u, json.NewDecoder(res.Body).Decode(&u)
}

func (c *Client) Create(ctx context.Context, name string) error {
	body := strings.NewReader(fmt.Sprintf(`{"name": %q}`, name))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/users", body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusCreated {
		return fmt.Errorf("users: create: %s", res.Status)
	}

	return nil
}
```

## A server per test

Each test starts its own server, so tests can run in parallel without sharing mocks. `Journal("")` keeps a journal of the requests in a temporary directory, which `Close` removes, and `Silent()` keeps the test output clean.

```go
func newServer(t *testing.T) *mockhttp.Server {
	t.Helper()

	m := mockhttp.MustNew(mockhttp.Setup().Journal("").Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	return m
}
```

## The happy path

The mock matches the path and the token, and answers JSON. `AssertCalled` fails the test when the client never called it, such as when it built a wrong URL.

```go
func TestGetUser(t *testing.T) {
	m := newServer(t)
	scoped := m.MustMock(mockhttp.Getf("/users/42").
		Header("Authorization", match.Equal("Bearer secret")).
		Reply(mockhttp.OK().JSON(map[string]any{"id": 42, "name": "ann"})))

	client := &Client{BaseURL: m.URL(), Token: "secret"}
	u, err := client.Get(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "ann" {
		t.Errorf("name = %q, want ann", u.Name)
	}

	scoped.AssertCalled(t)
}
```

## An error answer

`Getf` takes a glob, so `/users/*` answers any user. The test checks that the client turns a `404` into an error.

```go
func TestGetUserNotFound(t *testing.T) {
	m := newServer(t)
	m.MustMock(mockhttp.Getf("/users/*").Reply(mockhttp.NotFound()))

	client := &Client{BaseURL: m.URL(), Token: "secret"}
	if _, err := client.Get(t.Context(), 7); err == nil {
		t.Fatal("want an error for a 404")
	}
}
```

A request no mock matches gets `418` with a `VLatte-Error: no-match` header, never a status a real API sends, and a body that lists the mocks that came closest: see [Troubleshooting](/http/troubleshooting).

## What the client sent

The journal verifies requests after the fact, whatever answered them. `AssertRequested` checks how many requests a query selects, here by their method, path and a field of their JSON body, and `AssertNotRequested` that no request went unmatched.

```go
func TestCreateUserSendsTheName(t *testing.T) {
	m := newServer(t)
	m.MustMock(mockhttp.Postf("/users").Reply(mockhttp.Created()))

	client := &Client{BaseURL: m.URL(), Token: "secret"}
	if err := client.Create(t.Context(), "bob"); err != nil {
		t.Fatal(err)
	}

	m.AssertRequested(t.Context(), t, 1, journal.Query{
		Method: match.Equal("POST"),
		Path:   match.Equal("/users"),
		Body:   match.JSONPath("$.name", match.Equal("bob")),
	})
	m.AssertNotRequested(t.Context(), t, journal.Query{Outcome: journal.OutcomeUnmatched})
}
```

## What you used

- `mockhttp.MustNew`, `Setup().Journal`, `Setup().Silent`: [HTTP](/http/), [Request journal](/features/journal).
- `Getf`, `Postf`, `Header`: [Mocks](/http/mocks), [Request matching](/http/matching).
- `OK().JSON`, `NotFound`, `Created`: [Replies](/http/replies).
- `match.Equal`, `match.JSONPath`: [Matchers](/matchers/).
