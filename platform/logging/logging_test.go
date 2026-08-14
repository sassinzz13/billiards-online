package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestLoggerReturnsDefaultWhenContextIsEmpty(t *testing.T) {
	if got := Logger(context.Background()); got == nil {
		t.Fatal("Logger(empty ctx) = nil, want slog.Default()")
	}
}

// Attributes attached with With must appear on every line logged from the derived context. This is
// what makes a log line from inside a match traceable to the connection that caused it.
func TestWithAccumulatesAttributes(t *testing.T) {
	var buf bytes.Buffer
	ctx := WithLogger(context.Background(), New(&buf, "info", "json"))

	ctx = With(ctx, KeyRequestID, "req-1")
	ctx = With(ctx, KeyMatchID, "match-9")
	Logger(ctx).Info("shot resolved")

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log output is not valid JSON: %v\n%s", err, buf.String())
	}
	if line[KeyRequestID] != "req-1" {
		t.Errorf("%s = %v, want req-1", KeyRequestID, line[KeyRequestID])
	}
	if line[KeyMatchID] != "match-9" {
		t.Errorf("%s = %v, want match-9", KeyMatchID, line[KeyMatchID])
	}
	if line["msg"] != "shot resolved" {
		t.Errorf("msg = %v, want %q", line["msg"], "shot resolved")
	}
}

// A derived context must not leak its attributes back into its parent.
func TestWithDoesNotMutateParentContext(t *testing.T) {
	var buf bytes.Buffer
	parent := WithLogger(context.Background(), New(&buf, "info", "json"))

	_ = With(parent, KeyUserID, "user-1")
	Logger(parent).Info("from parent")

	if strings.Contains(buf.String(), "user-1") {
		t.Errorf("parent logger inherited a child attribute:\n%s", buf.String())
	}
}

func TestNewRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, "warn", "json").Info("should be filtered")

	if buf.Len() != 0 {
		t.Errorf("info line emitted at warn level:\n%s", buf.String())
	}
}

func TestNewTextFormat(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, "info", "text").Info("hello")

	out := buf.String()
	if !strings.Contains(out, "msg=hello") {
		t.Errorf("text output = %q, want it to contain msg=hello", out)
	}
	if json.Valid(buf.Bytes()) {
		t.Error("text format produced JSON")
	}
}

func TestParseLevel(t *testing.T) {
	tests := map[string]slog.Level{
		"debug":    slog.LevelDebug,
		"info":     slog.LevelInfo,
		"WARN":     slog.LevelWarn,
		"error":    slog.LevelError,
		"nonsense": slog.LevelInfo, // config validates; this is the safe fallback
	}
	for in, want := range tests {
		if got := parseLevel(in); got != want {
			t.Errorf("parseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}
