# Testing webhooks

This guide mocks a payment API that calls your service back once a payment is made, and checks that the callback came. It uses [webhooks](/webhooks/), [templates](/templating/) and server data.

## Two servers

One server stands for the payment API, the other for your service, which gets the callback. In a real test, your service is the code under test and only the payment API is a mock; here a second mock server stands in for it, so the test can check what the webhook sent.

```go
func TestPayingAnOrderCallsTheShopBack(t *testing.T) {
	// shop stands for your service: it receives the payment API's callback.
	shop := mockhttp.MustNew(mockhttp.Setup().Silent())
	shop.MustStart()
	t.Cleanup(shop.Close)

	callback := shop.MustMock(mockhttp.Postf("/payments/*/status").
		Body(match.JSONPath("$.status", match.Equal("paid"))).
		Reply(mockhttp.NoContent()))

	// payments stands for the payment API.
	wh, err := webhook.New()
	require.NoError(t, err)

	payments := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
	payments.MustStart()
	t.Cleanup(payments.Close)
	payments.SetData(map[string]any{"shop": shop.URL()})

	payments.MustMock(mockhttp.Postf("/payments").
		OnCompleteEvent(webhook.Options().
			Method(http.MethodPost).
			URLTemplate(`{{ .Data.shop }}/payments/{{ (fromJSON .Request.Body.String).id }}/status`).
			Header("Content-Type", "application/json").
			Body(`{"status": "paid"}`).
			Delay(100 * time.Millisecond).
			Build("webhook")).
		Reply(mockhttp.Accepted()))

	res, err := http.Post(payments.URL("/payments"), "application/json", strings.NewReader(`{"id": "p-1"}`))
	require.NoError(t, err)
	io.Copy(io.Discard, res.Body)
	res.Body.Close()

	require.Equal(t, http.StatusAccepted, res.StatusCode)
	callback.AssertNumberOfCalls(t, 1)
}
```

## What makes it work

- **The listener.** A mock names the listener of its `on_complete` event, here `webhook`, so the server registers a `webhook.WebHook` under that name first. The `vlatte` command registers one as `webhook` for you.
- **Server data.** `SetData` gives templates `.Data`. The callback's address is only known once the shop server started, so it goes in data, and the webhook's `URLTemplate` reads it.
- **The request.** The URL template reads the payment's id from the request body: `fromJSON` parses it, so the webhook calls `/payments/p-1/status`.
- **No waiting.** The response ends once its webhooks are sent, delay included, so the test checks the callback right after it read the body. Read the body to its end first: a large or streamed body sends its headers before the webhooks are done.

## In a mock file

The same mock, for the `vlatte` command. The command has no data to read, so the callback's address is written out, here as a shop on port 9000:

```yaml
request:
  method: POST
  path: /payments

response:
  status: 202

on_complete:
  - name: webhook
    parameters:
      method: POST
      url_template: 'http://localhost:9000/payments/{{ (fromJSON .Request.Body.String).id }}/status'
      header:
        content-type: application/json
      body: '{"status": "paid"}'
      delay: 100ms
```

## When the callback fails

A webhook that can't reach its target, or gets a status outside `2xx`, doesn't change the mock's reply: the payment still answers `202`. The server logs the failure as a warning, and the [journal](/more/journal) keeps it in the request's `Message`, one line for each event, so a test checks a webhook failed without reading the logs. A test of the happy path asserts on the callback server, as above.
