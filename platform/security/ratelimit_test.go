package security

import (
	"sync"
	"testing"
	"time"
)

func TestAllowsBurstThenBlocks(t *testing.T) {
	l := NewRateLimiter(1, 3, time.Minute)
	defer l.Close()

	for i := range 3 {
		if !l.Allow("ip") {
			t.Fatalf("request %d denied within the burst of 3", i+1)
		}
	}
	if l.Allow("ip") {
		t.Error("request 4 allowed — the burst should be exhausted")
	}
}

func TestRefillsOverTime(t *testing.T) {
	l := NewRateLimiter(1, 2, time.Minute) // 1 token/sec
	defer l.Close()

	start := time.Now()
	l.allowAt("ip", start)
	l.allowAt("ip", start)
	if l.allowAt("ip", start) {
		t.Fatal("burst not exhausted")
	}

	// Half a second is not enough for a whole token.
	if l.allowAt("ip", start.Add(500*time.Millisecond)) {
		t.Error("allowed after 0.5s at 1 token/sec")
	}
	// A full second is.
	if !l.allowAt("ip", start.Add(1500*time.Millisecond)) {
		t.Error("denied after 1.5s at 1 token/sec")
	}
}

func TestRefillIsCappedAtBurst(t *testing.T) {
	l := NewRateLimiter(1, 2, time.Minute)
	defer l.Close()

	start := time.Now()
	l.allowAt("ip", start)

	// After a long idle period the bucket must be full, not overflowing — otherwise waiting an
	// hour would buy an hour's worth of attempts in one go.
	later := start.Add(time.Hour)
	if !l.allowAt("ip", later) || !l.allowAt("ip", later) {
		t.Fatal("bucket did not refill to burst")
	}
	if l.allowAt("ip", later) {
		t.Error("bucket refilled beyond burst — an idle attacker would bank attempts")
	}
}

// Keys must be independent, or one abusive client would lock out everyone else.
func TestKeysAreIndependent(t *testing.T) {
	l := NewRateLimiter(1, 1, time.Minute)
	defer l.Close()

	if !l.Allow("a") {
		t.Fatal("first request for key a denied")
	}
	if l.Allow("a") {
		t.Fatal("second request for key a allowed")
	}
	if !l.Allow("b") {
		t.Error("key b denied because key a was exhausted")
	}
}

func TestResetClearsABucket(t *testing.T) {
	l := NewRateLimiter(1, 1, time.Minute)
	defer l.Close()

	l.Allow("ip")
	if l.Allow("ip") {
		t.Fatal("burst not exhausted")
	}

	// A successful login calls Reset, so someone who mistyped their password is not left limited.
	l.Reset("ip")
	if !l.Allow("ip") {
		t.Error("Allow() = false after Reset()")
	}
}

// The sweeper must reclaim buckets, including ones belonging to a rate-limited key that went quiet.
// Tokens are only topped up on access, so a naive "is it full?" check would never collect those and
// the limiter would leak an entry per attacking IP.
func TestSweepReclaimsIdleBuckets(t *testing.T) {
	l := NewRateLimiter(1000, 2, 20*time.Millisecond)
	defer l.Close()

	l.Allow("quiet")
	l.Allow("quiet") // exhausted, then never touched again
	if l.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", l.Len())
	}

	deadline := time.Now().Add(2 * time.Second)
	for l.Len() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if l.Len() != 0 {
		t.Errorf("Len() = %d after the sweep interval, want 0 — exhausted idle buckets leak", l.Len())
	}
}

func TestCloseStopsTheSweeper(t *testing.T) {
	l := NewRateLimiter(1, 1, 10*time.Millisecond)
	l.Close()
	// Close blocks until the goroutine exits, so a second call to Len must not race or panic.
	_ = l.Len()
}

func TestConcurrentAllowIsRaceFree(t *testing.T) {
	l := NewRateLimiter(100, 50, time.Minute)
	defer l.Close()

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			l.Allow("shared")
			l.Allow(string(rune('a' + n%26)))
		}(i)
	}
	wg.Wait()
}
