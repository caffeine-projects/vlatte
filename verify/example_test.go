package verify_test

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/verify"
)

// exampleT reports what fails on the standard output, as a test's *testing.T reports it in the test's log.
type exampleT struct{}

func (exampleT) Helper()                        {}
func (exampleT) Logf(format string, a ...any)   { fmt.Printf(format+"\n", a...) }
func (exampleT) Error(a ...any)                 { fmt.Println(a...) }
func (exampleT) Errorf(format string, a ...any) { fmt.Printf(format+"\n", a...) }

// post sends a POST of path to m, as the code under test does.
func post(m *mockhttp.Server, path, body string) {
	res, err := http.Post(m.URL(path), "application/json", strings.NewReader(body))
	if err != nil {
		panic(err)
	}

	res.Body.Close()
}

func Example() {
	t := exampleT{} // a test's *testing.T

	m := mockhttp.MustNew(mockhttp.Setup().Silent().Journal(""))
	m.MustStart()
	defer m.Close()

	m.MustMock(mockhttp.Postf("/orders").Reply(mockhttp.Created()))

	post(m, "/orders", `{"item": "tea"}`)
	post(m, "/orders", `{"item": "coffee"}`)

	orders := m.VerifyRequests().Method("POST").Path(match.Equal("/orders"))

	fmt.Println(orders.Times(t, 2))
	fmt.Println(orders.Body(match.JSONPath("$.item", match.Equal("tea"))).Times(t, 1))
	fmt.Println(m.VerifyRequests().Status(match.Equal(http.StatusNotFound)).Never(t))
	// Output:
	// true
	// true
	// true
}

func ExampleRequests_Within() {
	t := exampleT{} // a test's *testing.T

	m := mockhttp.MustNew(mockhttp.Setup().Silent().Journal(""))
	m.MustStart()
	defer m.Close()

	// The code under test calls the server back in the background, as a webhook does.
	go func() {
		res, err := http.Post(m.URL("/hooks/paid"), "application/json", strings.NewReader(`{"order": 7}`))
		if err == nil {
			res.Body.Close()
		}
	}()

	fmt.Println(m.VerifyRequests().Path(match.Equal("/hooks/paid")).Within(5*time.Second).Times(t, 1))
	// Output: true
}

func ExampleRequests_Since() {
	t := exampleT{} // a test's *testing.T

	m := mockhttp.MustNew(mockhttp.Setup().Silent().Journal(""))
	m.MustStart()
	defer m.Close()

	post(m, "/orders", "") // another test's request, on the server the tests share

	mark := m.JournalMark()
	post(m, "/orders", "")

	fmt.Println(m.VerifyRequests().Since(mark).Path(match.Equal("/orders")).Times(t, 1))
	// Output: true
}

func ExampleInOrder() {
	t := exampleT{} // a test's *testing.T

	m := mockhttp.MustNew(mockhttp.Setup().Silent().Journal(""))
	m.MustStart()
	defer m.Close()

	post(m, "/login", "")
	post(m, "/orders", "")
	post(m, "/logout", "")

	login := m.VerifyRequests().Path(match.Equal("/login"))
	logout := m.VerifyRequests().Path(match.Equal("/logout"))

	fmt.Println(verify.InOrder(t, login, logout))
	// Output: true
}
