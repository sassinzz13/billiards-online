package main

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/sassinzz13/billiards-online/platform/logging"
)

// headerRequestID is both read and written, so a request ID assigned by Traefik or a client is
// preserved end to end rather than replaced.
const headerRequestID = "X-Request-Id"

// requestID attaches a request identifier to the context and echoes it in the response.
//
// Every log line produced while handling the request carries it, which is what makes a single
// request traceable across features (§47).
func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(headerRequestID)
		if id == "" || len(id) > 64 {
			// Reject oversized client-supplied IDs rather than logging them: an unbounded value
			// from the client would end up in every log line for this request.
			id = newRequestID()
		}

		c.Writer.Header().Set(headerRequestID, id)
		c.Request = c.Request.WithContext(
			logging.With(c.Request.Context(), logging.KeyRequestID, id),
		)
		c.Next()
	}
}

func newRequestID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is not recoverable, but a request ID is diagnostic rather than
		// security-critical, so fall back to a timestamp instead of failing the request.
		return "ts-" + time.Now().UTC().Format("20060102T150405.000000000")
	}
	return hex.EncodeToString(b[:])
}

// requestLogger emits one structured line per request, after it completes.
//
// Health and readiness probes are logged at debug level: they fire every few seconds and would
// otherwise bury real traffic.
func requestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		// The request context already carries the request ID from the requestID middleware.
		c.Request = c.Request.WithContext(
			logging.WithLogger(c.Request.Context(), logger.With(
				logging.KeyRequestID, c.Writer.Header().Get(headerRequestID),
			)),
		)

		c.Next()

		status := c.Writer.Status()
		attrs := []any{
			"method", c.Request.Method,
			"path", path,
			"status", status,
			"durationMs", time.Since(start).Milliseconds(),
			"bytes", c.Writer.Size(),
		}
		// The client IP is useful for abuse investigation but is personal data, so it is recorded
		// only when something actually went wrong.
		if status >= http.StatusBadRequest {
			attrs = append(attrs, "clientIp", c.ClientIP())
		}

		log := logging.Logger(c.Request.Context())
		switch {
		case status >= http.StatusInternalServerError:
			log.Error("request failed", attrs...)
		case status >= http.StatusBadRequest:
			log.Warn("request rejected", attrs...)
		case isProbe(path):
			log.Debug("request", attrs...)
		default:
			log.Info("request", attrs...)
		}
	}
}

func isProbe(path string) bool {
	return path == "/health" || path == "/ready" || path == "/api/v1/health"
}

// recovery turns a panic into a 500 without taking the process down.
//
// The panic value and stack go to the log; the client receives an opaque error. Leaking a stack
// trace would expose internal structure and is prohibited by §42 and §51.
func recovery(logger *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, recovered any) {
		logging.Logger(c.Request.Context()).Error("panic recovered",
			"panic", recovered,
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"stack", string(stack()),
		)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code":    "internal",
			"message": "An internal error occurred.",
		}})
	})
}

// secureHeaders sets response headers that cost nothing and close off whole classes of attack.
//
// HSTS is deliberately absent here: it is set by Traefik, which terminates TLS and therefore knows
// whether the connection was actually secure.
func secureHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// The API serves only JSON, so no source of any kind should ever be loaded from it.
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		c.Next()
	}
}
