package webhook_test

import (
	"context"
	"fmt"
	"net/http"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/webhook"
)

func Example() {
	// The service the webhook calls.
	target := mockhttp.MustNew(mockhttp.Setup().Silent())
	target.MustStart()
	defer target.Close()

	hook := target.MustMock(mockhttp.Postf("/hooks/orders").
		Body(match.Equal("order 42 shipped")).
		Reply(mockhttp.NoContent()))

	wh, err := webhook.New()
	if err != nil {
		panic(err)
	}

	// The server registers the webhook as an on-complete listener by name; a mock names it to send it.
	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
	m.MustStart()
	defer m.Close()

	m.MustMock(mockhttp.Putf("/orders/42/ship").
		OnCompleteEvent(webhook.Options().
			Method(http.MethodPost).
			URL(target.URL("/hooks/orders")).
			BodyTemplate("order {{ index .Request.URLPathSegments 1 }} shipped").
			Build("webhook")).
		Reply(mockhttp.OK()))

	req, err := http.NewRequest(http.MethodPut, m.URL("/orders/42/ship"), nil)
	if err != nil {
		panic(err)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}

	res.Body.Close()

	// The response waits for the webhook, so the target has it once the call returns.
	fmt.Println(res.StatusCode, hook.Hits())
	// Output: 200 1
}

func ExampleWithTransform() {
	target := mockhttp.MustNew(mockhttp.Setup().Silent())
	target.MustStart()
	defer target.Close()

	hook := target.MustMock(mockhttp.Postf("/hooks").
		Headerf("X-Signature", "signed").
		Reply(mockhttp.NoContent()))

	// A Transform changes the request of each send, here to add a header the mock doesn't set.
	wh, err := webhook.New(webhook.WithTransform(func(_ context.Context, _ vlatte.OnComplete, args *webhook.Args) error {
		args.Header.Set("X-Signature", "signed")
		return nil
	}))
	if err != nil {
		panic(err)
	}

	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
	m.MustStart()
	defer m.Close()

	m.MustMock(mockhttp.Getf("/ping").
		OnCompleteEvent(webhook.Options().Method(http.MethodPost).URL(target.URL("/hooks")).Build("webhook")).
		Reply(mockhttp.OK()))

	res, err := http.Get(m.URL("/ping"))
	if err != nil {
		panic(err)
	}

	res.Body.Close()

	fmt.Println(hook.Hits())
	// Output: 1
}
