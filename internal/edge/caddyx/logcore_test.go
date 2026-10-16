package caddyx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"go.uber.org/zap/zapcore"

	"github.com/hyprsh/tnl/internal/logx"
)

func capture(t *testing.T, jsonOut bool) (zapcore.Core, *bytes.Buffer) {
	var buf bytes.Buffer
	lg := logx.New(logx.Options{Level: slog.LevelDebug, JSON: jsonOut, Out: &buf})
	return newSlogCore(func() *slog.Logger { return lg }), &buf
}

func write(t *testing.T, c zapcore.Core, name string, lvl zapcore.Level, msg string, fields ...zapcore.Field) {
	t.Helper()
	ent := zapcore.Entry{LoggerName: name, Level: lvl, Message: msg}
	if ce := c.Check(ent, nil); ce != nil {
		ce.Write(fields...)
		return
	}
	t.Fatalf("core refused %s at %s", name, lvl)
}

func TestCoreKeepsLoggerNameAndFieldsInTextAndJSON(t *testing.T) {
	c, buf := capture(t, false)
	write(t, c.With([]zapcore.Field{{Key: "a", Type: zapcore.StringType, String: "1"}}),
		"admin.api", zapcore.InfoLevel, "hello", zapcore.Field{Key: "b", Type: zapcore.StringType, String: "2"})
	for _, w := range []string{"level=info", "msg=hello", "logger=admin.api", "a=1", "b=2"} {
		if !strings.Contains(buf.String(), w) {
			t.Errorf("text line %q lacks %q", buf.String(), w)
		}
	}

	c, buf = capture(t, true)
	write(t, c, "admin.api", zapcore.WarnLevel, "hello")
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["logger"] != "admin.api" || m["level"] != "warn" {
		t.Errorf("json line %v", m)
	}
}

func TestCoreLevelMap(t *testing.T) {
	for _, tc := range []struct {
		name string
		lvl  zapcore.Level
		msg  string
		want string
	}{
		{"tls.obtain", zapcore.InfoLevel, "obtaining certificate", "debug"},
		{"tls.obtain", zapcore.ErrorLevel, "will retry", "debug"},
		{"tls.renew", zapcore.InfoLevel, "renewing", "debug"},
		{"tls.issuance.acme", zapcore.InfoLevel, "waiting", "debug"},
		{"tls.issuance.acme.acme_client", zapcore.InfoLevel, "trying", "debug"},
		{"tls.cache.maintenance", zapcore.InfoLevel, "certificate is in renewal window", "debug"},
		{"http.log.error", zapcore.ErrorLevel, "dial tcp: refused", "debug"},
		{"http.handlers.reverse_proxy", zapcore.ErrorLevel, "aborting", "debug"},
		{"http.stdlib", zapcore.ErrorLevel, "http: TLS handshake error", "debug"},
		{"tls.issuance.acme", zapcore.ErrorLevel, "no ACME server answered", "error"},
		{"http.stdlib", zapcore.ErrorLevel, "http: Accept error: too many open files", "error"},
		{"tls", zapcore.WarnLevel, "something real", "warn"},
		{"tls.cache.maintenance", zapcore.ErrorLevel, "other failure", "error"},
		{"admin", zapcore.InfoLevel, "x", "info"},
		{"x", zapcore.DPanicLevel, "x", "error"},
		{"x", zapcore.DebugLevel, "x", "debug"},
	} {
		c, buf := capture(t, false)
		write(t, c, tc.name, tc.lvl, tc.msg)
		if !strings.Contains(buf.String(), "level="+tc.want+" ") {
			t.Errorf("%s %s %q: line %q, want level %s", tc.name, tc.lvl, tc.msg, buf.String(), tc.want)
		}
	}
}

func TestCoreRespectsTheLoggersLevel(t *testing.T) {
	var buf bytes.Buffer
	lg := logx.New(logx.Options{Level: slog.LevelInfo, Out: &buf})
	c := newSlogCore(func() *slog.Logger { return lg })
	// A demoted entry is checked at the level it will be written at.
	if ce := c.Check(zapcore.Entry{LoggerName: "tls.obtain", Level: zapcore.InfoLevel}, nil); ce != nil {
		t.Error("a demoted entry passed an info logger")
	}
	if ce := c.Check(zapcore.Entry{LoggerName: "admin", Level: zapcore.InfoLevel}, nil); ce == nil {
		t.Error("an info entry did not pass an info logger")
	}
}
