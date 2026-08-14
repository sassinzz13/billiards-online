package main

import (
	"net/http"
	"net/http/pprof"
	"runtime"
	"runtime/debug"
)

// stack returns the current goroutine's stack, for the recovery middleware.
func stack() []byte { return debug.Stack() }

// newInternalRouter builds the operator-only listener.
//
// This must never be routed publicly. pprof exposes heap contents, goroutine stacks, and enough
// timing information to be a genuine information leak, so it binds to a separate address that
// Traefik has no route to. platform/config refuses to start if it shares the public listener.
//
// Phase 19 adds /metrics here. Nothing else belongs on this listener.
func newInternalRouter() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)

	// A cheap liveness signal plus the two numbers most worth watching early: goroutine count
	// catches leaks (§40), and heap size catches allocation problems in the physics loop (§41).
	mux.HandleFunc("GET /debug/vitals", func(w http.ResponseWriter, r *http.Request) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"goroutines":` + itoa(runtime.NumGoroutine()) +
			`,"heapAllocBytes":` + itoa(int(m.HeapAlloc)) +
			`,"gcCycles":` + itoa(int(m.NumGC)) + `}`))
	})

	return mux
}

// itoa avoids pulling strconv in for three integers.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
