package mockgrpc

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"reflect"
	"sync"

	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

var (
	_ credentials.TransportCredentials = (*serverCreds)(nil)
	_ net.Conn                         = (*cutConn)(nil)
)

// errConnCut is the error of a write to a connection a mock cut.
var errConnCut = errors.New("grpc: a mock cut the connection")

// _cutKey is the trailer key that marks the end of a call a mock cut: its connection is cut before the trailer goes
// out, so no client reads it.
const _cutKey = "vlatte-cut"

// The HTTP/2 frames a connection reads in what grpc writes on it, RFC 9113 § 4.1 and § 6.
const (
	_frameHeadLen      = 9
	_frameHeaders      = 0x1
	_frameContinuation = 0x9
	_flagEndHeaders    = 0x4
	_flagPadded        = 0x8
	_flagPriority      = 0x20
	_priorityLen       = 5
	_headerTableSize   = 4096 // the HPACK table size grpc encodes with, which a client can only lower
)

// cuts reports whether err, a reply's, asks to cut the call: it matches vlatte.ErrAbort or vlatte.ErrReset.
func cuts(err error) bool {
	return errors.Is(err, vlatte.ErrAbort) || errors.Is(err, vlatte.ErrReset)
}

// cut answers call, whose mock's reply cut it with cause: the mock counts the call and its events run, then the
// connection of the call is cut after what the reply sent, before its status. grpc can't reset one stream from a
// handler, so the connection goes, and with it the other calls it carries. A server whose ServerOptions set grpc.Creds
// handshakes its connections with those: it can't cut them, and the call fails.
func (in *interceptors) cut(ctx context.Context, call *Call, mock *Mock, h *hit, cause error) error {
	app := in.app
	kind := typeOf(call.desc)

	conn := app.creds.connOf(ctx)
	if conn == nil {
		err := fmt.Errorf("cutting a call needs the server's own credentials, not those of grpc.Creds: %w", cause)

		h.takeBack()
		callOf(ctx).failed(fmt.Errorf("%s: %w", kind, err))
		app.publish(ctx, adminapi.EventError, call.method, mock, codes.Internal, err)

		return replyError(ctx, kind.String(), mock, err)
	}

	// The client never reads this status: it is what the journal and the events see.
	answer := status.Errorf(codes.Unavailable, "%s: %s: the mock cut the call: %v", kind, mock, cause)

	token := rand.Text()
	conn.cutAt(token, cause)
	_ = grpc.SetTrailer(ctx, metadata.Pairs(_cutKey, token))

	h.count()
	app.afterServed(ctx, mock, call, answer)
	app.publish(ctx, adminapi.EventMatched, call.method, mock, codes.Unavailable, nil)

	return answer
}

// ServerHandshake handshakes rawConn with the credentials, and returns the connection grpc serves calls on, which
// a mock can cut: see cutConn.
func (c *serverCreds) ServerHandshake(rawConn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	conn, info, err := c.TransportCredentials.ServerHandshake(rawConn)
	if err != nil {
		return nil, nil, err
	}

	cc := newCutConn(conn, rawConn, func(addr net.Addr) { c.conns.Delete(addr) })
	c.conns.Store(cc.addr, cc)

	return cc, info, nil
}

// connOf returns the connection of the call of ctx, nil when the server didn't handshake it.
func (c *serverCreds) connOf(ctx context.Context) *cutConn {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil
	}

	conn, _ := c.conns.Load(p.Addr)
	cc, _ := conn.(*cutConn)

	return cc
}

// connAddr is the address of a connection whose own address may be that of others too, such as an in-memory one:
// a call finds its connection by the address of its peer.
type connAddr struct{ net.Addr }

// cutConn is a connection grpc serves calls on, which a mock can cut after what it sent. It reads the HTTP/2 frames
// grpc writes, and decodes their header blocks, so it finds the trailer of a call that asked for a cut, and cuts the
// connection before it: what grpc wrote before goes out, and nothing after.
//
// A header block goes out once whole, after this connection decoded it. A connection whose frames it fails to read
// passes them on unread, and cuts at once.
type cutConn struct {
	net.Conn

	raw    net.Conn // the connection under TLS, if any, which the cut closes
	addr   net.Addr // a key of the connection: see connAddr
	closed func(addr net.Addr)

	mu     sync.Mutex
	cuts   map[string]error // the cause of each cut, by the token of the trailer it waits for
	found  error            // the cause of the cut whose trailer the block being decoded holds
	cause  error            // the cause of the cut, once the connection is cut
	broken bool             // the frames couldn't be read: they pass on unread

	dec     *hpack.Decoder
	head    []byte // the head of the frame being read, until it is whole
	inFrame bool   // the head is whole: left bytes of the frame are still to come
	left    int    // the bytes of the frame still to come
	flags   byte
	block   bool   // the frame is part of a header block: it is held
	held    []byte // the frames of the header block being read
	start   int    // where the frame starts in held
}

// newCutConn returns conn, the connection grpc serves calls on, over raw, which closed is told of once it closes.
func newCutConn(conn, raw net.Conn, closed func(addr net.Addr)) *cutConn {
	addr := conn.RemoteAddr()
	if reflect.ValueOf(addr).Kind() != reflect.Pointer {
		addr = &connAddr{addr}
	}

	c := &cutConn{Conn: conn, raw: raw, addr: addr, closed: closed, cuts: make(map[string]error)}
	c.dec = hpack.NewDecoder(_headerTableSize, c.emit)

	return c
}

// RemoteAddr returns the address of the peer, which tells the connection apart from others.
func (c *cutConn) RemoteAddr() net.Addr {
	return c.addr
}

// Close closes the connection.
func (c *cutConn) Close() error {
	c.closed(c.addr)
	return c.Conn.Close()
}

// Write writes p, but a header block that ends a call a mock cut, and what follows it: the connection is cut there.
func (c *cutConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cause != nil {
		return 0, errConnCut
	}

	if err := c.scan(p); err != nil {
		return 0, err
	}

	return len(p), nil
}

// cutAt has the connection cut, with cause, before the trailer that carries token goes out.
func (c *cutConn) cutAt(token string, cause error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch {
	case c.cause != nil:
	case c.broken:
		c.cut(cause)
	default:
		c.cuts[token] = cause
	}
}

// scan reads the frames in p: it passes them on, or holds those of a header block until it is whole.
func (c *cutConn) scan(p []byte) error {
	for len(p) > 0 {
		if c.broken {
			_, err := c.Conn.Write(p)
			return err
		}

		if !c.inFrame {
			n := min(_frameHeadLen-len(c.head), len(p))
			c.head, p = append(c.head, p[:n]...), p[n:]

			if len(c.head) < _frameHeadLen {
				return nil
			}

			if err := c.startFrame(); err != nil {
				return err
			}
		}

		n := min(c.left, len(p))
		if c.block {
			c.held = append(c.held, p[:n]...)
		} else if _, err := c.Conn.Write(p[:n]); err != nil {
			return err
		}

		c.left, p = c.left-n, p[n:]

		if c.left == 0 {
			if err := c.endFrame(); err != nil {
				return err
			}
		}
	}

	return nil
}

// startFrame starts the frame whose head was read: one of a header block is held, any other passes on.
func (c *cutConn) startFrame() error {
	head := c.head
	c.head = c.head[:0]
	c.inFrame = true
	c.left = int(head[0])<<16 | int(head[1])<<8 | int(head[2])
	c.flags = head[4]

	typ := head[3]
	c.block = typ == _frameHeaders || typ == _frameContinuation

	switch {
	case c.block:
		c.start = len(c.held)
		c.held = append(c.held, head...)

		return nil
	case len(c.held) > 0:
		// A header block goes on in CONTINUATION frames only: these frames can't be read.
		return c.giveUp(head)
	default:
		_, err := c.Conn.Write(head)
		return err
	}
}

// endFrame ends the frame read whole. A header block is decoded as its frames come, and passes on once whole, unless it
// ends a call a mock cut: the connection is cut then.
func (c *cutConn) endFrame() error {
	c.inFrame = false
	if !c.block {
		return nil
	}

	frame := c.held[c.start:]
	fragment := frame[_frameHeadLen:]

	if frame[3] == _frameHeaders {
		if c.flags&_flagPadded != 0 {
			if len(fragment) == 0 || int(fragment[0]) >= len(fragment) {
				return c.giveUp(nil)
			}

			fragment = fragment[1 : len(fragment)-int(fragment[0])]
		}

		if c.flags&_flagPriority != 0 {
			if len(fragment) < _priorityLen {
				return c.giveUp(nil)
			}

			fragment = fragment[_priorityLen:]
		}
	}

	if _, err := c.dec.Write(fragment); err != nil {
		return c.giveUp(nil)
	}

	if c.flags&_flagEndHeaders == 0 {
		return nil
	}

	if err := c.dec.Close(); err != nil {
		return c.giveUp(nil)
	}

	if c.found != nil {
		c.cut(c.found)
		return errConnCut
	}

	_, err := c.Conn.Write(c.held)
	c.held = c.held[:0]

	return err
}

// emit reads a field of the header block being decoded: the trailer of a cut.
func (c *cutConn) emit(f hpack.HeaderField) {
	if f.Name != _cutKey {
		return
	}

	if cause, ok := c.cuts[f.Value]; ok {
		c.found = cause
	}
}

// giveUp passes on, unread, what the connection holds, then rest, and the frames that follow: cuts happen at once.
func (c *cutConn) giveUp(rest []byte) error {
	c.broken = true

	_, err := c.Conn.Write(append(c.held, rest...))
	c.held = nil

	for _, cause := range c.cuts {
		if c.cause == nil {
			c.cut(cause)
		}
	}

	if c.cause != nil {
		return errConnCut
	}

	return err
}

// cut cuts the connection, with cause. A cause that matches vlatte.ErrReset resets it, where TCP allows. Any other ends
// the server's side, where TCP allows, so the client reads an end: grpc closes the connection once the client did.
// Closing it at once would reset it, if the client sent what grpc didn't read yet.
func (c *cutConn) cut(cause error) {
	c.cause = cause

	if errors.Is(cause, vlatte.ErrReset) {
		if l, ok := c.raw.(interface{ SetLinger(sec int) error }); ok {
			_ = l.SetLinger(0)
		}
	} else if w, ok := c.raw.(interface{ CloseWrite() error }); ok && w.CloseWrite() == nil {
		return
	}

	_ = c.raw.Close()
}
