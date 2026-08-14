import { TestBed } from '@angular/core/testing';

import { Envelope, RealtimeService } from './realtime.service';

/**
 * A minimal stand-in for the browser's WebSocket, since jsdom does not implement a connectable
 * one. Each instance is recorded in FakeWebSocket.instances so a test can grab "the socket the
 * service just opened" and drive it — trigger onopen, push a message, simulate a drop — exactly as
 * the real handshake and frames would.
 */
class FakeWebSocket {
  static readonly instances: FakeWebSocket[] = [];
  static readonly OPEN = 1;
  static readonly CONNECTING = 0;
  static readonly CLOSED = 3;

  readyState = FakeWebSocket.CONNECTING;
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  readonly sent: string[] = [];
  readonly url: string;

  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
  }

  send(data: string): void {
    this.sent.push(data);
  }

  close(): void {
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.();
  }

  // --- test-only driving methods, not part of the real WebSocket surface ---

  simulateOpen(): void {
    this.readyState = FakeWebSocket.OPEN;
    this.onopen?.();
  }

  simulateMessage(envelope: Envelope): void {
    this.onmessage?.({ data: JSON.stringify(envelope) });
  }

  simulateRawMessage(data: string): void {
    this.onmessage?.({ data });
  }

  /** An unexpected drop — the server closed it, or the network died. Not a client-initiated close. */
  simulateUnexpectedClose(): void {
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.();
  }
}

describe('RealtimeService', () => {
  let originalWebSocket: typeof WebSocket;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    FakeWebSocket.instances.length = 0;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    globalThis.WebSocket = FakeWebSocket as any;
    TestBed.configureTestingModule({});
  });

  afterEach(() => {
    globalThis.WebSocket = originalWebSocket;
    vi.useRealTimers();
  });

  function service(): RealtimeService {
    return TestBed.inject(RealtimeService);
  }

  function latestSocket(): FakeWebSocket {
    return FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
  }

  it('starts idle and transitions through connecting to open', () => {
    const svc = service();
    expect(svc.state()).toBe('idle');

    svc.connect();
    expect(svc.state()).toBe('connecting');

    latestSocket().simulateOpen();
    expect(svc.state()).toBe('open');
    expect(svc.isOpen()).toBe(true);
  });

  it('does not open a second socket on a repeated connect() call', () => {
    const svc = service();
    svc.connect();
    latestSocket().simulateOpen();

    svc.connect();
    expect(FakeWebSocket.instances.length).toBe(1);
  });

  it('decodes and emits well-formed envelopes on messages$', () => {
    const svc = service();
    svc.connect();
    latestSocket().simulateOpen();

    const received: Envelope[] = [];
    svc.messages$.subscribe((e) => received.push(e));

    latestSocket().simulateMessage({ v: 1, type: 'auth.success', seq: 1 });

    expect(received).toEqual([{ v: 1, type: 'auth.success', seq: 1 }]);
  });

  it('tracks the highest seq seen, ignoring out-of-order regressions', () => {
    const svc = service();
    svc.connect();
    latestSocket().simulateOpen();

    latestSocket().simulateMessage({ v: 1, type: 'x', seq: 3 });
    expect(svc.lastSeq()).toBe(3);

    // A message with a lower seq must not move lastSeq backward.
    latestSocket().simulateMessage({ v: 1, type: 'x', seq: 2 });
    expect(svc.lastSeq()).toBe(3);

    latestSocket().simulateMessage({ v: 1, type: 'x', seq: 7 });
    expect(svc.lastSeq()).toBe(7);
  });

  // A malformed message from the server would be a server bug, but the client must not throw and
  // tear down the whole message stream over one bad frame.
  it('ignores unparseable messages without emitting or throwing', () => {
    const svc = service();
    svc.connect();
    latestSocket().simulateOpen();

    const received: Envelope[] = [];
    svc.messages$.subscribe((e) => received.push(e));

    expect(() => latestSocket().simulateRawMessage('not json')).not.toThrow();
    expect(() => latestSocket().simulateRawMessage('{"missing":"type and v"}')).not.toThrow();
    expect(received).toEqual([]);
  });

  it('sends envelopes with the protocol version filled in automatically', () => {
    const svc = service();
    svc.connect();
    latestSocket().simulateOpen();

    svc.send({ type: 'ping' });

    expect(latestSocket().sent).toEqual([JSON.stringify({ v: 1, type: 'ping' })]);
  });

  it('silently drops a send when the socket is not open', () => {
    const svc = service();
    svc.connect(); // still 'connecting', not yet open
    svc.send({ type: 'ping' });

    expect(latestSocket().sent).toEqual([]);
  });

  it('does not reconnect after a deliberate disconnect()', () => {
    vi.useFakeTimers();
    const svc = service();
    svc.connect();
    latestSocket().simulateOpen();

    svc.disconnect();
    expect(svc.state()).toBe('closed');

    vi.advanceTimersByTime(60_000);
    expect(FakeWebSocket.instances.length).toBe(1); // no reconnect attempt was ever made
  });

  it('reconnects with backoff after an unexpected close', () => {
    vi.useFakeTimers();
    const svc = service();
    svc.connect();
    latestSocket().simulateOpen();

    latestSocket().simulateUnexpectedClose();
    expect(svc.state()).toBe('reconnecting');
    expect(FakeWebSocket.instances.length).toBe(1); // not yet — the backoff delay hasn't elapsed

    vi.advanceTimersByTime(5000);
    expect(FakeWebSocket.instances.length).toBe(2); // the reconnect attempt opened a new socket
  });

  it('resets the backoff counter after a successful reconnect', () => {
    vi.useFakeTimers();
    const svc = service();
    svc.connect();
    latestSocket().simulateOpen();

    // First drop and reconnect.
    latestSocket().simulateUnexpectedClose();
    vi.advanceTimersByTime(5000);
    latestSocket().simulateOpen();
    expect(svc.state()).toBe('open');

    // A second drop right after should use the SHORT initial delay again, not a longer one carried
    // over from the first backoff sequence — proof the counter actually reset on success.
    latestSocket().simulateUnexpectedClose();
    vi.advanceTimersByTime(1300); // just past the ~1s base delay (with jitter headroom)
    expect(FakeWebSocket.instances.length).toBe(3);
  });
});
