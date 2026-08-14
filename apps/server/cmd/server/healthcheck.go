package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// runHealthcheck probes this process's own /health endpoint and exits 0 or 1.
//
// It exists because the server image is built FROM scratch: there is no shell, no curl, and no
// wget, so a Docker HEALTHCHECK has nothing to call. Rather than fattening the image with a
// userland just to run a probe, the binary probes itself.
//
// Invoked as `server -healthcheck`. Liveness only — it deliberately hits /health rather than
// /ready, so a database outage does not cause the orchestrator to restart a healthy process.
func runHealthcheck() int {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	// HTTP_ADDR is a listen address (":8080"), which is not dialable as-is.
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: bad HTTP_ADDR %q: %v\n", addr, err)
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/health")
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: status %d\n", resp.StatusCode)
		return 1
	}
	return 0
}
