package realtime

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/sassinzz13/billiards-online/game/protocol"
	pws "github.com/sassinzz13/billiards-online/platform/websocket"
)

// connection is one authenticated, upgraded WebSocket session. It owns the read loop and the
// server→client sequence counter; platform/websocket.Conn owns the transport underneath it.
type connection struct {
	conn   *pws.Conn
	userID uuid.UUID
	connID uuid.UUID
	logger *slog.Logger

	seq atomic.Uint64
}

func (c *connection) nextSeq() uint64 {
	return c.seq.Add(1)
}

// serve sends the initial auth.success envelope, then reads and dispatches until the connection
// closes for any reason — client disconnect, protocol violation, or backpressure.
func (c *connection) serve(ctx context.Context) {
	if env, err := protocol.NewAuthSuccess(c.nextSeq(), c.userID.String(), c.connID.String()); err == nil {
		c.send(env)
	} else {
		c.logger.Error("encode auth.success", "error", err)
	}

	// Watches for backpressure independently of the blocking Read below. Close is safe to call
	// concurrently with an in-progress Read (platform/websocket.Conn's underlying coder/websocket
	// guarantees this — see its Conn doc comment: "All methods may be called concurrently except
	// for Reader and Read"), so this goroutine can act the moment the outbound queue overflows
	// without waiting for the read loop to come up for air. It is owned by this call: ctx is
	// cancelled by the gateway's deferred cancel() no later than the moment serve returns, so the
	// goroutine's lifetime is bounded by the connection's own (§40).
	go func() {
		select {
		case <-c.conn.Full():
			c.logger.Warn("closing connection: outbound queue full")
			c.conn.Close(pws.StatusSlowClient, "slow client")
		case <-ctx.Done():
		}
	}()

	for {
		data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		c.handle(data)
	}
}

// handle decodes one client frame and dispatches it by type.
//
// No client-originated message type exists yet — gameplay messages arrive from Phase 6 onward.
// Every type reaching the switch below is therefore "unknown" right now, which is exactly the
// behaviour Phase 5 needs to prove: an unrecognized type gets an error envelope, never a silent
// drop (docs/protocol.md §2).
func (c *connection) handle(data []byte) {
	env, err := protocol.Decode(data)
	if err != nil {
		c.logger.Warn("malformed frame", "error", err)
		if errEnv, encErr := protocol.NewError(c.nextSeq(), protocol.ErrCodeMalformed,
			"Message could not be parsed.", ""); encErr == nil {
			c.send(errEnv)
		}
		// A frame that does not even parse as an envelope leaves nothing to safely resume
		// dispatch from — the byte stream framing itself is untrusted at that point, not just this
		// one message. Close rather than continue reading.
		//
		// CloseAfterDrain, not Close: Close writes its close frame immediately and independently of
		// the outbound queue, which can — and, verified directly, sometimes does — deliver the
		// close frame to the client before the write pump gets to the error envelope just enqueued
		// above, silently breaking "explain, then close." Routing the close through the same queue
		// guarantees the explanation arrives first.
		c.conn.CloseAfterDrain(websocket.StatusUnsupportedData, "malformed frame")
		return
	}

	if env.V != protocol.Version {
		c.logger.Warn("unsupported protocol version", "version", env.V)
		if errEnv, encErr := protocol.NewError(c.nextSeq(), protocol.ErrCodeVersion,
			"Unsupported protocol version.", env.RequestID); encErr == nil {
			c.send(errEnv)
		}
		c.conn.CloseAfterDrain(websocket.StatusProtocolError, "unsupported protocol version")
		return
	}

	switch env.Type {
	default:
		c.logger.Info("unknown message type", "type", env.Type)
		if errEnv, encErr := protocol.NewError(c.nextSeq(), protocol.ErrCodeUnknownType,
			"Unrecognized message type.", env.RequestID); encErr == nil {
			c.send(errEnv)
		}
	}
}

// send encodes and enqueues an envelope. It never blocks: if the outbound queue is already full,
// the backpressure watcher goroutine in serve will close the connection independently — send does
// not need to duplicate that decision here (§23).
func (c *connection) send(env protocol.Envelope) {
	data, err := protocol.Encode(env)
	if err != nil {
		c.logger.Error("encode outbound envelope", "error", err, "type", env.Type)
		return
	}
	if !c.conn.Send(data) {
		c.logger.Warn("outbound queue full while sending", "type", env.Type)
	}
}
