package adminapi

import (
	"bytes"
	"net/http"
	"sync"
)

// _streamBuffer is how many messages a client of a stream can fall behind before it misses some.
const _streamBuffer = 256

// stream fans messages out to the clients of a Server-Sent Events route, as they come. It keeps none: a client gets
// what comes while it listens. A client that falls behind misses messages, so publishers never wait for clients.
type stream struct {
	mu      sync.Mutex
	clients map[chan streamMessage]struct{}
	closed  bool
}

// streamMessage is a message of a stream: an event of a type, and its data.
type streamMessage struct {
	event string
	data  []byte
}

func newStream() *stream {
	return &stream{clients: make(map[chan streamMessage]struct{})}
}

// publish sends a message to every client that keeps up. It never waits.
func (s *stream) publish(event string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for c := range s.clients {
		select {
		case c <- streamMessage{event: event, data: data}:
		default:
		}
	}
}

// listening reports whether a client listens, so publishers make no message no one gets.
func (s *stream) listening() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.clients) > 0
}

// close ends the stream of every client, and of those that come after.
func (s *stream) close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true
	for c := range s.clients {
		close(c)
		delete(s.clients, c)
	}
}

func (s *stream) subscribe() (chan streamMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, false
	}

	c := make(chan streamMessage, _streamBuffer)
	s.clients[c] = struct{}{}

	return c, true
}

func (s *stream) unsubscribe(c chan streamMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.clients[c]; ok {
		close(c)
		delete(s.clients, c)
	}
}

// serve streams the messages to the client of r, as Server-Sent Events, until it leaves or the stream closes.
func (s *stream) serve(w http.ResponseWriter, r *http.Request) {
	c, ok := s.subscribe()
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	defer s.unsubscribe(c)

	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	if rc.Flush() != nil {
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case m, ok := <-c:
			if !ok {
				return
			}

			if _, err := w.Write(m.encode()); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

// encode writes the message as a Server-Sent Event: a data line per line of its data.
func (m streamMessage) encode() []byte {
	var b bytes.Buffer
	b.WriteString("event: ")
	b.WriteString(m.event)
	b.WriteByte('\n')

	for line := range bytes.SplitSeq(bytes.TrimRight(m.data, "\n"), []byte("\n")) {
		b.WriteString("data: ")
		b.Write(line)
		b.WriteByte('\n')
	}

	b.WriteByte('\n')

	return b.Bytes()
}
