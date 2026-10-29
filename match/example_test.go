package match_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

// matches reports whether m matches v, as a mock server asks it of a part of a request.
func matches(m vlatte.Matcher, v any) bool {
	ok, err := m.Match(vlatte.NewMatcherArg(context.Background(), vlatte.NewExplanation(), v))
	if err != nil {
		panic(err)
	}

	return ok
}

func Example() {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	defer m.Close()

	// Each matcher matches a part of the request: a header, a query value, a field of the JSON body.
	m.MustMock(mockhttp.Postf("/orders").
		Header("Authorization", match.HasPrefix("Bearer ")).
		Query("priority", match.IsIn([]string{"low", "high"})).
		Body(match.JSONPath("$.items[0].qty", match.GreaterThan(0))).
		Reply(mockhttp.Created()))

	send := func(query, body string) int {
		req, err := http.NewRequest(http.MethodPost, m.URL("/orders?"+query), strings.NewReader(body))
		if err != nil {
			panic(err)
		}

		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("Content-Type", "application/json")

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}

		res.Body.Close()

		return res.StatusCode
	}

	fmt.Println(send("priority=high", `{"items": [{"qty": 2}]}`))
	fmt.Println(send("priority=urgent", `{"items": [{"qty": 2}]}`))
	fmt.Println(send("priority=low", `{"items": [{"qty": 0}]}`))
	// Output:
	// 201
	// 418
	// 418
}

func ExampleEqual() {
	// Text read as a number equals the number, whatever the Go type of either.
	fmt.Println(matches(match.Equal(10), "10"))
	fmt.Println(matches(match.Equal(10), 10.0))
	fmt.Println(matches(match.Equal(10), "010")) // not the JSON number grammar

	// Text read as a bool equals the bool.
	fmt.Println(matches(match.Equal(true), "1"))

	// StrictEqual reads no text.
	fmt.Println(matches(match.StrictEqual(10), "10"))
	// Output:
	// true
	// true
	// false
	// true
	// false
}

func ExampleAll() {
	// A version tag: starts with v, has no spaces, and isn't a release candidate.
	tag := match.All(
		match.HasPrefix("v"),
		match.Not(match.Contain(" ")),
		match.Not(match.Contain("-rc")),
	)

	for _, v := range []string{"v1.2.0", "v1.3.0-rc.1", "1.2.0"} {
		fmt.Println(v, matches(tag, v))
	}
	// Output:
	// v1.2.0 true
	// v1.3.0-rc.1 false
	// 1.2.0 false
}

func ExampleJSONPath() {
	body := `{"user": {"name": "Ada", "roles": ["admin", "dev"]}}`

	fmt.Println(matches(match.JSONPath("$.user.name", match.Equal("Ada")), body))
	fmt.Println(matches(match.JSONPath("$.user.roles", match.Contain("admin")), body))
	fmt.Println(matches(match.JSONPath("$.user.email", match.Present()), body))
	// Output:
	// true
	// true
	// false
}

func ExampleEqualJSON() {
	// The order of the members doesn't matter, nor, with IgnoreArrayOrder, that of the items.
	expected := match.EqualJSON(`{"id": 1, "tags": ["a", "b"]}`, match.IgnoreArrayOrder())

	fmt.Println(matches(expected, `{"tags": ["b", "a"], "id": 1}`))
	fmt.Println(matches(expected, `{"tags": ["b", "a"], "id": 1, "extra": true}`))
	// Output:
	// true
	// false
}
