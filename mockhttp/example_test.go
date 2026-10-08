package mockhttp_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"github.com/caffeine-projects/vlatte/match"
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

func Example_multipart() {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	defer m.Close()

	// Match an upload by its fields and files, and reply with a multipart body.
	m.MustMock(mockhttp.Postf("/upload").
		FormField("title", match.Equal("Holiday")).
		FormFile("photo", mockhttp.FileWith().Filename(match.HasSuffix(".png"))).
		Reply(mockhttp.Created().Multipart(mockhttp.MultipartMixed(
			mockhttp.NewPart().ContentType("text/plain").BodyText("saved"),
		).Boundary("b"))))

	// A Multipart writes request bodies too.
	contentType, body, err := mockhttp.MultipartForm(
		mockhttp.NewPart().Name("title").BodyText("Holiday"),
		mockhttp.NewPart().Name("photo").Filename("me.png").Body([]byte("PNG")),
	).Encode(nil, nil)
	if err != nil {
		panic(err)
	}

	res, err := http.Post(m.URL("/upload"), contentType, bytes.NewReader(body))
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()

	reply, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}

	fmt.Println(res.StatusCode, res.Header.Get(mockhttp.HeaderContentType))
	fmt.Printf("%q\n", reply)
	// Output:
	// 201 multipart/mixed; boundary=b
	// "--b\r\nContent-Type: text/plain\r\n\r\nsaved\r\n--b--\r\n"
}
