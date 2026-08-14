package websocket_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coder "github.com/coder/websocket"

	pws "github.com/sassinzz13/billiards-online/platform/websocket"
)

// server accepts exactly one connection per test and hands it to fn, running fn in its own
// goroutine so the test can drive a real client against it concurrently.
func server(t *testing.T, fn func(ctx context.Context, c *pws.Conn)) *httptest.Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := pws.Accept(ctx, w, r)
		if err != nil {
			t.Logf("accept: %v", err)
			return
		}
		fn(ctx, conn)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func dial(t *testing.T, srv *httptest.Server) *coder.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, _, err := coder.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close(coder.StatusNormalClosure, "") })
	return c
}

func TestSendAndReadRoundTrip(t *testing.T) {
	srv := server(t, func(ctx context.Context, c *pws.Conn) {
		// Closing cleanly once the handler is done lets the client's own Close complete its
		// handshake immediately, rather than waiting out coder/websocket's 5s default handshake
		// timeout against a peer that never replies — the connection would still be correct either
		// way, but leaving it implicit just makes every test in this file slow.
		defer c.Close(coder.StatusNormalClosure, "")

		msg, err := c.Read(ctx)
		if err != nil {
			t.Errorf("server Read() = %v", err)
			return
		}
		if !c.Send(append([]byte("echo:"), msg...)) {
			t.Error("Send() reported the queue full on the first message")
		}
		time.Sleep(50 * time.Millisecond) // give the write pump a moment to flush before closing
	})
	client := dial(t, srv)

	if err := client.Write(context.Background(), coder.MessageText, []byte("hello")); err != nil {
		t.Fatalf("client write: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, data, err := client.Read(ctx)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(data) != "echo:hello" {
		t.Errorf("got %q, want %q", data, "echo:hello")
	}
}

// The read limit is what stands between a malicious or broken client and unbounded memory growth
// (§42, §59). coder/websocket enforces it during Read and fails the connection; this test proves
// that failure actually happens at the configured boundary rather than trusting the library's
// documentation alone.
func TestOversizedFrameFailsTheConnection(t *testing.T) {
	srv := server(t, func(ctx context.Context, c *pws.Conn) {
		if _, err := c.Read(ctx); err == nil {
			t.Error("server Read() succeeded on an oversized frame, want an error")
		}
	})
	client := dial(t, srv)

	oversized := make([]byte, pws.ReadLimitBytes+1)
	writeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// The write may itself fail once the server aborts the connection mid-frame, or may succeed if
	// it races ahead of the server's enforcement — either is consistent with "the connection did
	// not accept an oversized message"; only the server-side assertion above is load-bearing.
	_ = client.Write(writeCtx, coder.MessageBinary, oversized)
}

// Reject binary frames outright: every message this protocol defines is JSON text (ADR 0006).
func TestBinaryFrameIsRejected(t *testing.T) {
	srv := server(t, func(ctx context.Context, c *pws.Conn) {
		defer c.Close(coder.StatusNormalClosure, "")
		if _, err := c.Read(ctx); err == nil {
			t.Error("server Read() accepted a binary frame, want a rejection")
		}
	})
	client := dial(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Write(ctx, coder.MessageBinary, []byte{1, 2, 3}); err != nil {
		t.Fatalf("client write: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // let the server-side Read observe and return
}

// This is the Phase 5 exit criterion: a deliberately slow client is closed with a policy code
// rather than being allowed to grow a queue. The client here dials and then never reads again,
// simulating a peer that stopped draining its receive side.
func TestSlowClientTriggersBackpressureClose(t *testing.T) {
	closed := make(chan coder.StatusCode, 1)
	// Signalled once the server's own Close call returns, so the test can wait for the graceful
	// close handshake to finish on both sides before tearing the client down. Without this, the
	// client goroutine below can report the close code and let the test proceed to its deferred
	// CloseNow() — a hard TCP reset — while the server is still in the middle of Close()'s own
	// handshake read, which then fails with "connection reset by peer". That is a race in this
	// test's cleanup ordering, not a bug in Conn: it showed up reliably under `go test -race`,
	// where the added synchronization overhead was enough to flip the usual ordering.
	serverClosed := make(chan struct{})

	srv := server(t, func(ctx context.Context, c *pws.Conn) {
		defer close(serverClosed)

		// Flood well past the queue capacity. A real caller (internal/realtime) would be sending
		// one shot.result at a time as gameplay produces them; here the flood stands in for "the
		// client fell behind" without needing a slow consumer on the wire to actually take real
		// time to prove.
		full := false
		for i := 0; i < pws.OutboundQueueSize*4 && !full; i++ {
			if !c.Send([]byte("filler")) {
				full = true
			}
		}
		if !full {
			t.Error("Send() never reported the queue full after flooding well past its capacity")
			return
		}

		select {
		case <-c.Full():
			// This is the policy under test: the caller — not Conn itself — decides to close once
			// notified, exactly as platform/websocket's package doc promises.
			if err := c.Close(pws.StatusSlowClient, "slow client"); err != nil {
				t.Logf("close: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("Full() was never signalled")
		}
	})

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, err := coder.Dial(dialCtx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.CloseNow()

	go func() {
		// Close() does not go through Conn's outbound queue — it writes the close frame directly
		// on the underlying connection, which coder/websocket allows concurrently with the write
		// pump's own in-flight Write calls (Conn's doc comment: "All methods may be called
		// concurrently except for Reader and Read"). So the close frame can arrive interleaved with
		// or after some of the ~64 already-queued filler messages, not necessarily as the very next
		// frame. A slow client in reality would not be reading at all; this loop exists only to
		// drain past whatever filler arrived first and observe the eventual close code.
		for {
			_, _, err := client.Read(context.Background())
			var closeErr coder.CloseError
			if errors.As(err, &closeErr) {
				closed <- closeErr.Code
				return
			}
			if err != nil {
				closed <- 0
				return
			}
			// A successful data read (one of the filler messages) — keep draining.
		}
	}()

	select {
	case code := <-closed:
		if code != pws.StatusSlowClient {
			t.Errorf("close code = %v, want %v (StatusSlowClient)", code, pws.StatusSlowClient)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("connection was never closed")
	}

	select {
	case <-serverClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("server-side Close() never returned")
	}
}

func TestFullChannelIsClosedOnlyOnce(t *testing.T) {
	srv := server(t, func(ctx context.Context, c *pws.Conn) {
		defer c.Close(pws.StatusSlowClient, "")
		for i := 0; i < pws.OutboundQueueSize*2; i++ {
			c.Send([]byte("x")) // deliberately ignore the return value; calling past "full" repeatedly
		}
		// Reading Full() twice must not panic (closing an already-closed channel would).
		select {
		case <-c.Full():
		default:
			t.Fatal("Full() was not signalled after flooding past capacity")
		}
		select {
		case <-c.Full():
		default:
			t.Fatal("Full() should still read as closed on a second check")
		}
	})
	dial(t, srv)
	time.Sleep(100 * time.Millisecond)
}

// CloseAfterDrain exists specifically to guarantee this ordering. A plain Close call races the
// write pump directly — verified by temporarily swapping the call below for c.Close and watching
// this test fail, which is how the bug behind this fix was actually found while wiring
// internal/realtime's malformed-frame handling.
func TestCloseAfterDrainDeliversQueuedMessageFirst(t *testing.T) {
	srv := server(t, func(ctx context.Context, c *pws.Conn) {
		if !c.Send([]byte("last words")) {
			t.Error("Send() reported the queue full on an empty queue")
		}
		if !c.CloseAfterDrain(pws.StatusSlowClient, "done") {
			t.Error("CloseAfterDrain() reported the queue full on an empty queue")
		}
	})
	client := dial(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, data, err := client.Read(ctx)
	if err != nil {
		t.Fatalf("expected to read the queued message before any close, got: %v", err)
	}
	if string(data) != "last words" {
		t.Errorf("got %q, want %q", data, "last words")
	}

	// The connection closes immediately afterward.
	if _, _, err := client.Read(ctx); err == nil {
		t.Error("connection still open after the queued close instruction, want it closed")
	}
}
