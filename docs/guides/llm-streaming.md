# Testing an LLM streaming client

This guide tests a Go client of a chat API that streams its answers: it puts the answer together as the chunks arrive, fails when the stream drops midway, and fails on a rate limit. It uses [event streams](/sse/), [faults](/http/faults) and [request matching](/http/matching).

## The client

`complete` asks for a completion with `"stream": true`, in the shape of OpenAI's chat completions API, and reads the answer from the `data` of each event until `[DONE]`:

```go
// complete asks for a chat completion, streamed, and returns the answer once the stream sends [DONE].
func complete(ctx context.Context, client *http.Client, url, prompt string) (string, error) {
	body := fmt.Sprintf(`{"model": "gpt-test", "stream": true, "messages": [{"role": "user", "content": %q}]}`, prompt)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer sk-test")

	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", res.StatusCode)
	}

	var answer strings.Builder
	lines := bufio.NewScanner(res.Body)
	for lines.Scan() {
		data, ok := strings.CutPrefix(lines.Text(), "data: ")
		if !ok {
			continue // a blank line between events, or another field
		}
		if data == "[DONE]" {
			return answer.String(), nil
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return "", err
		}
		for _, c := range chunk.Choices {
			answer.WriteString(c.Delta.Content)
		}
	}
	if err := lines.Err(); err != nil {
		return "", err
	}

	return "", errors.New("the stream ended before [DONE]")
}
```

## A streamed answer

The mock matches the client's key and a request that asks to stream, and sends three chunks of JSON, 20 ms apart, then `[DONE]`. `chunk` builds each event, so the tests read as the stream does:

```go
// chunk is an event of a streamed chat completion, with a piece of the answer.
func chunk(content string) *mockhttp.Event {
	return mockhttp.NewEvent().DataJSON(map[string]any{
		"object":  "chat.completion.chunk",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": content}}},
	})
}

func TestPutsTheAnswerTogether(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(mockhttp.Postf("/v1/chat/completions").
		Header("Authorization", match.Equal("Bearer sk-test")).
		Body(match.JSONPath("$.stream", match.Equal(true))).
		Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(
			chunk("Hel"), chunk("lo, "), chunk("world"),
			mockhttp.NewEvent().Data("[DONE]"),
		).EventDelay(20 * time.Millisecond))))

	answer, err := complete(t.Context(), http.DefaultClient, m.URL(), "Say hello")
	if err != nil {
		t.Fatal(err)
	}
	if answer != "Hello, world" {
		t.Fatalf("answer = %q", answer)
	}

	scoped.AssertCalled(t)
}
```

Each chunk goes as one `data:` line of compact JSON, flushed as it is sent, so the client reads it while the next one waits:

```text
data: {"choices":[{"delta":{"content":"Hel"},"index":0}],"object":"chat.completion.chunk"}

```

A request with `"stream": false` doesn't match this mock. To test a client that asks for both, add a mock that matches `false` and answers the whole completion as JSON.

## A stream that drops

A connection can drop in the middle of an answer. `fault.LimitData(100)` sends the first 100 bytes of the stream, the first chunk and part of the second, then closes the connection. A client that returns what it got so far would hand on half an answer: `complete` must fail.

```go
func TestFailsWhenTheStreamDrops(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Postf("/v1/chat/completions").
		Step(fault.LimitData(100)).
		Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(
			chunk("Hel"), chunk("lo, "), chunk("world"),
			mockhttp.NewEvent().Data("[DONE]"),
		))))

	answer, err := complete(t.Context(), http.DefaultClient, m.URL(), "Say hello")
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, answer = %q", err, answer)
	}
}
```

## A rate limit

An API answers `429` before any stream starts, with a JSON error. The client must not read it as an empty stream:

```go
func TestFailsOnARateLimit(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Postf("/v1/chat/completions").
		Reply(mockhttp.Status(http.StatusTooManyRequests).
			Header("Retry-After", "1").
			JSON(map[string]any{"error": map[string]any{"type": "rate_limit_error", "message": "slow down"}})))

	_, err := complete(t.Context(), http.DefaultClient, m.URL(), "Say hello")
	if err == nil || err.Error() != "status 429" {
		t.Fatalf("err = %v", err)
	}
}
```

## The same mock in a file

A [mock file](/http/mock-files) keeps the stream with the tests' other mocks, and the [CLI](/run/cli) serves it to a client that isn't in Go. In `data`, an object is sent as JSON, and text as it is:

```yaml
request:
  method: POST
  path: /v1/chat/completions
  header: {authorization: "Bearer sk-test"}
  body: [jsonpath, $.stream, [equal, true]]
response:
  status: 200
  event_stream:
    event_delay: 20ms
    events:
      - data: {object: chat.completion.chunk, choices: [{index: 0, delta: {content: "Hel"}}]}
      - data: {object: chat.completion.chunk, choices: [{index: 0, delta: {content: "lo, "}}]}
      - data: {object: chat.completion.chunk, choices: [{index: 0, delta: {content: "world"}}]}
      - data: "[DONE]"
```

## Named events

Anthropic's Messages API names each event after the type of its data, and sends pings between them. A client of it dispatches on the name: it builds the text from `content_block_delta` events, skips `ping`, and stops at `message_stop`. `event` names each event:

```yaml
request:
  method: POST
  path: /v1/messages
  header: {x-api-key: test-key, anthropic-version: "2023-06-01"}
  body: [jsonpath, $.stream, [equal, true]]
response:
  status: 200
  event_stream:
    events:
      - {event: message_start, data: {type: message_start, message: {id: msg_1, role: assistant, content: []}}}
      - {event: content_block_start, data: {type: content_block_start, index: 0, content_block: {type: text, text: ""}}}
      - {event: ping, data: {type: ping}}
      - {event: content_block_delta, data: {type: content_block_delta, index: 0, delta: {type: text_delta, text: Hello}}}
      - {event: content_block_delta, data: {type: content_block_delta, index: 0, delta: {type: text_delta, text: ", world"}}}
      - {event: content_block_stop, data: {type: content_block_stop, index: 0}}
      - {event: message_delta, data: {type: message_delta, delta: {stop_reason: end_turn}}}
      - {event: message_stop, data: {type: message_stop}}
```

A client that splits events at blank lines, and reads both `event:` and `data:` lines, gets `Hello, world` from it.
