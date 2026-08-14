// Package websocket wraps coder/websocket with this project's defaults: a bounded read size, a
// bounded outbound queue with a dedicated write pump, and a backpressure policy that closes rather
// than blocks or drops.
//
// It knows nothing about the message envelope or gameplay — those live in game/protocol and
// internal/realtime respectively. This package is transport only (§7): connection lifecycle, read
// limits, and the write pump. No business logic, no auth.
package websocket

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// ReadLimitBytes bounds a single incoming frame. A client that sends more than this is almost
// certainly broken or hostile — coder/websocket enforces it during Read and fails the connection,
// which this package surfaces as a normal read error for the caller to close on (§42, §59).
const ReadLimitBytes = 32 * 1024

// OutboundQueueSize bounds how many messages may be queued for a connection before it is
// considered too slow to keep talking to. There is no unbounded channel anywhere in this system
// (§23, §40).
const OutboundQueueSize = 64

// WriteTimeout bounds a single write to the socket. A write that cannot complete in this long is
// as good as a dead connection.
const WriteTimeout = 10 * time.Second

// PingInterval and PongTimeout implement the idle liveness check documented in
// docs/protocol.md §1: a connection that has gone silent — cable pulled, laptop asleep — is
// detected and closed rather than left as a phantom that never receives a reply.
const (
	PingInterval = 30 * time.Second
	PongTimeout  = 10 * time.Second
)

// Policy close codes. 1000-1015 are reserved by RFC 6455; 3000-3999 is where libraries/frameworks
// are meant to define their own (see coder/websocket's StatusCode doc comment).
const (
	// StatusSlowClient is sent when a connection's outbound queue is full — the backpressure
	// policy from §23: close and let the client resync, never drain, never drop silently.
	StatusSlowClient websocket.StatusCode = 3000
)

// Conn is one WebSocket connection: the accepted socket, its bounded outbound queue, and the write
// pump goroutine that owns writing to it.
//
// Reading is deliberately not owned by this type. The caller's own goroutine calls Read in a loop —
// there is no separate "read pump" goroutine, because coder/websocket's Read is already the
// blocking primitive a read loop needs, and adding a second goroutine plus a channel to relay
// messages would be indirection with no benefit. The write pump exists because writes need to be
// serialized against the bounded queue to implement backpressure; reads do not have an equivalent
// requirement.
type Conn struct {
	ws       *websocket.Conn
	outbound chan outboundItem

	// full is signalled at most once, when Send finds the queue full. The caller (internal/realtime)
	// is expected to select on Done() and close the connection with StatusSlowClient — Conn does not
	// close itself, because only the caller knows the right log line and metric to attach.
	full     chan struct{}
	fullOnce sync.Once

	done chan struct{} // closed when the write pump exits, for callers that want to wait for it
}

// outboundItem is either a data frame or an instruction to close once every item queued ahead of it
// has been written. Both travel through the same channel so ordering is automatic: the write pump
// is a single FIFO consumer, so a message enqueued by Send is always written before a close queued
// afterward by CloseAfterDrain is acted on.
type outboundItem struct {
	data        []byte // nil for a close instruction
	closeCode   websocket.StatusCode
	closeReason string
}

func (i outboundItem) isClose() bool { return i.data == nil }

// Accept upgrades an HTTP request to a WebSocket connection and starts its write pump bound to ctx.
//
// originPatterns is intentionally not used for coder/websocket's own OriginPatterns option.
// internal/realtime performs its own exact-match Origin check via platform/config.AllowsOrigin
// before ever calling Accept (ADR 0009's reasoning: a prefix/suffix or glob match is how this check
// gets bypassed), so Accept is called with InsecureSkipVerify to avoid checking twice with two
// different matching semantics that could one day disagree.
func Accept(ctx context.Context, w http.ResponseWriter, r *http.Request) (*Conn, error) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return nil, fmt.Errorf("accept websocket: %w", err)
	}
	ws.SetReadLimit(ReadLimitBytes)

	c := &Conn{
		ws:       ws,
		outbound: make(chan outboundItem, OutboundQueueSize),
		full:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go c.writePump(ctx)
	return c, nil
}

// Read blocks for the next text message. Binary messages are rejected — every message this
// protocol defines is JSON text (ADR 0006); a binary frame is either a broken client or a future
// protocol version this server does not speak yet, and either way the caller should treat it as a
// malformed frame.
func (c *Conn) Read(ctx context.Context) ([]byte, error) {
	typ, data, err := c.ws.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageText {
		return nil, fmt.Errorf("unexpected message type %v, want text", typ)
	}
	return data, nil
}

// Send enqueues msg for the write pump without blocking. It reports false if the outbound queue
// was full, at which point the caller must close the connection with StatusSlowClient — Send itself
// never blocks, drops, or closes; it only ever reports the queue's state honestly (§23).
func (c *Conn) Send(msg []byte) bool {
	select {
	case c.outbound <- outboundItem{data: msg}:
		return true
	default:
		c.fullOnce.Do(func() { close(c.full) })
		return false
	}
}

// CloseAfterDrain queues a close instruction behind whatever is already enqueued, so a reply sent
// just before it (an error envelope explaining why the connection is about to close, say) is
// guaranteed to reach the client first.
//
// This exists because Close closes the underlying connection immediately and independently of the
// outbound queue — coder/websocket allows that concurrency, but "allowed" is not "ordered": a
// direct Close racing the write pump's draining of a just-enqueued message can deliver the close
// frame before the data frame, silently breaking "send an explanation, then close." Route ordering
// through the same queue the write pump already serializes, instead.
//
// Reports false if the queue was already full, in which case the connection is already on its way
// to closing via the backpressure path (Full) and this call is a safe no-op.
func (c *Conn) CloseAfterDrain(code websocket.StatusCode, reason string) bool {
	select {
	case c.outbound <- outboundItem{closeCode: code, closeReason: reason}:
		return true
	default:
		c.fullOnce.Do(func() { close(c.full) })
		return false
	}
}

// Full is closed the first time Send finds the outbound queue full. A connection handler selects
// on this alongside Read to notice backpressure even while blocked reading.
func (c *Conn) Full() <-chan struct{} {
	return c.full
}

// Done is closed when the write pump exits — the connection is no longer usable for writes.
func (c *Conn) Done() <-chan struct{} {
	return c.done
}

// Close closes the underlying connection with the given policy code. Safe to call more than once
// or concurrently with the write pump; coder/websocket's own Close is documented as safe for that.
func (c *Conn) Close(code websocket.StatusCode, reason string) error {
	return c.ws.Close(code, reason)
}

// writePump is the sole goroutine that ever writes to the socket, draining the outbound queue in
// order and pinging on an interval to detect a connection that has gone silent without ever
// formally closing (cable pulled, device slept). It owns its own lifetime: it exits and closes
// Done when ctx is cancelled, the queue is closed, or a write fails.
func (c *Conn) writePump(ctx context.Context) {
	defer close(c.done)

	ticker := time.NewTicker(PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case item, ok := <-c.outbound:
			if !ok {
				return
			}
			if item.isClose() {
				c.ws.Close(item.closeCode, item.closeReason)
				return
			}
			wctx, cancel := context.WithTimeout(ctx, WriteTimeout)
			err := c.ws.Write(wctx, websocket.MessageText, item.data)
			cancel()
			if err != nil {
				return
			}

		case <-ticker.C:
			pctx, cancel := context.WithTimeout(ctx, PongTimeout)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
