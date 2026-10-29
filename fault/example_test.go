package fault_test

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/caffeine-projects/vlatte/fault"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func ExampleConnectionResetByPeer() {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	defer m.Close()

	// A fault that writes what isn't HTTP goes with WirePipe.
	m.MustMock(mockhttp.Getf("/users").WirePipe(fault.ConnectionResetByPeer()).Reply(mockhttp.OK()))

	_, err := http.Get(m.URL("/users"))

	fmt.Println(errors.Is(err, syscall.ECONNRESET))
	// Output: true
}

func ExampleLimitData() {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	defer m.Close()

	// With Pipe, the limit counts the bytes of the body: the client gets the head, then 5 bytes of 11.
	m.MustMock(mockhttp.Getf("/report").Pipe(fault.LimitData(5)).Reply(mockhttp.OK().PlainText("hello world")))

	res, err := http.Get(m.URL("/report"))
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)

	fmt.Println(res.StatusCode, string(body))
	fmt.Println(errors.Is(err, io.ErrUnexpectedEOF))
	// Output:
	// 200 hello
	// true
}

func ExampleTimeout() {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	defer m.Close()

	// A timeout of 0 hangs until the server closes: the client's own timeout ends the wait.
	m.MustMock(mockhttp.Getf("/slow").WirePipe(fault.Timeout(0)).Reply(mockhttp.OK()))

	client := &http.Client{Timeout: 50 * time.Millisecond}

	_, err := client.Get(m.URL("/slow"))

	netErr, ok := errors.AsType[net.Error](err)
	fmt.Println(ok && netErr.Timeout())
	// Output: true
}
