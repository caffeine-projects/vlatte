package mockhttp_test

import (
	"fmt"
	"io"
	"net/http"

	"github.com/caffeine-projects/vlatte/mockhttp"
)

func Example() {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	defer m.Close()

	scoped := m.MustMock(mockhttp.Getf("/hello").Reply(mockhttp.OK().PlainText("hello world")))

	res, err := http.Get(m.URL("/hello"))
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}

	fmt.Println(res.StatusCode, string(body), scoped.Called())
	// Output: 200 hello world true
}
