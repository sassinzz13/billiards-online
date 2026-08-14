package security

import (
	"context"
	"sync"
	"time"
)

// RateLimiter is an in-memory token bucket, keyed by an arbitrary string (typically a client IP, or
// an IP plus an endpoint).
//
// In-memory is the right choice while a single process serves all traffic (§39): it needs no Redis,
// no network hop, and no operational surface. When the server becomes multi-instance the limit
// becomes per-instance, which is a real weakening — that is the point at which a shared limiter
// earns its keep, and not before.
//
// Buckets are created lazily and swept periodically. Without the sweep the map would grow without
// bound, which turns a defence against abuse into a memory exhaustion vector of its own (§40, §41).
type RateLimiter struct {
	rate  float64 // tokens per second
	burst float64
	ttl   time.Duration

	mu      sync.Mutex
	buckets map[string]*bucket

	stop func()
	done chan struct{}
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewRateLimiter returns a limiter allowing burst requests immediately, refilling at rate per
// second. Buckets idle for longer than ttl are discarded.
//
// The caller owns the returned limiter and must Close it, so the sweeper goroutine has a definite
// lifetime (§40).
func NewRateLimiter(rate float64, burst int, ttl time.Duration) *RateLimiter {
	ctx, cancel := context.WithCancel(context.Background())
	l := &RateLimiter{
		rate:    rate,
		burst:   float64(burst),
		ttl:     ttl,
		buckets: make(map[string]*bucket),
		stop:    cancel,
		done:    make(chan struct{}),
	}
	go l.sweep(ctx)
	return l
}

// Allow consumes one token for key, reporting whether the request may proceed.
func (l *RateLimiter) Allow(key string) bool {
	return l.allowAt(key, time.Now())
}

// allowAt is Allow with an injectable clock, so tests do not have to sleep.
func (l *RateLimiter) allowAt(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		// A new key starts full, minus the request being made right now.
		l.buckets[key] = &bucket{tokens: l.burst - 1, last: now}
		return true
	}

	// Refill for the time elapsed, capped at burst.
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Reset discards a key's bucket. Called after a successful login so that a legitimate user who
// mistyped their password a few times is not left rate limited.
func (l *RateLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}

// Close stops the sweeper and blocks until it has exited.
func (l *RateLimiter) Close() {
	l.stop()
	<-l.done
}

func (l *RateLimiter) sweep(ctx context.Context) {
	defer close(l.done)

	// Sweeping at the TTL means a bucket lives at most 2×TTL. Frequent enough to bound memory,
	// infrequent enough that the lock is essentially never contended by this goroutine.
	ticker := time.NewTicker(l.ttl)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			l.mu.Lock()
			for key, b := range l.buckets {
				// Drop a bucket once it would have refilled completely, because a full bucket is
				// indistinguishable from one that does not exist.
				//
				// The refill must be *computed* rather than read: tokens are only topped up when a
				// key is accessed, so a rate-limited attacker who goes quiet would otherwise keep a
				// permanently under-full bucket and leak an entry per key. Deleting on computed
				// refill is also not a way to escape the limit — waiting for a full refill is
				// exactly what serving the penalty means.
				if b.tokens+now.Sub(b.last).Seconds()*l.rate >= l.burst {
					delete(l.buckets, key)
				}
			}
			l.mu.Unlock()
		}
	}
}

// Len reports the number of tracked buckets. Test and diagnostic use only.
func (l *RateLimiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
