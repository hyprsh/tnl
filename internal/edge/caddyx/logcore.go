package caddyx

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"go.uber.org/zap/zapcore"
)

func init() {
	caddy.RegisterModule(LogCore{})
}

// LogCore is a caddy.logging.cores module for Caddy's default log. It turns
// Caddy's entries into tnl log lines. The default log's own writer is
// "discard", so this is the only place Caddy's entries go. The logger lives in
// the Runtime, not here, because a reload cleans up the old config's modules.
type LogCore struct {
	Edge string `json:"edge"`

	zapcore.Core `json:"-"`
}

func (LogCore) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "caddy.logging.cores.tnl",
		New: func() caddy.Module { return new(LogCore) },
	}
}

func (l *LogCore) Provision(ctx caddy.Context) error {
	rt, err := lookupRuntime(l.Edge)
	if err != nil {
		return err
	}
	l.Core = newSlogCore(rt.logger)
	return nil
}

var _ zapcore.Core = (*LogCore)(nil)

// slogCore is the zap-to-slog adapter. It holds no state beyond the fields
// added with With.
type slogCore struct {
	logger func() *slog.Logger
	fields []zapcore.Field
}

func newSlogCore(logger func() *slog.Logger) zapcore.Core { return &slogCore{logger: logger} }

// Enabled is true for every level: the level an entry is written at depends on
// its logger name, so Check decides.
func (c *slogCore) Enabled(zapcore.Level) bool { return true }

func (c *slogCore) With(fields []zapcore.Field) zapcore.Core {
	n := *c
	n.fields = append(append([]zapcore.Field(nil), c.fields...), fields...)
	return &n
}

func (c *slogCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if !c.logger().Enabled(context.Background(), slogLevel(ent)) {
		return ce
	}
	return ce.AddCore(ent, c)
}

func (c *slogCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	enc := zapcore.NewMapObjectEncoder()
	for _, f := range c.fields {
		f.AddTo(enc)
	}
	for _, f := range fields {
		f.AddTo(enc)
	}
	attrs := make([]any, 0, 2+2*len(enc.Fields))
	if ent.LoggerName != "" {
		attrs = append(attrs, "logger", ent.LoggerName)
	}
	for k, v := range enc.Fields {
		attrs = append(attrs, k, attrValue(v))
	}
	c.logger().Log(context.Background(), slogLevel(ent), ent.Message, attrs...)
	return nil
}

func (c *slogCore) Sync() error { return nil }

// attrValue keeps scalars as they are and renders nested values as JSON.
func attrValue(v any) any {
	switch v.(type) {
	case string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return v
	case error, fmt.Stringer:
		return fmt.Sprint(v)
	}
	if b, err := json.Marshal(v); err == nil {
		return string(b)
	}
	return fmt.Sprint(v)
}

// slogLevel is the level map: Caddy's levels, except that certificate obtain
// and renew chatter, Route errors and handshake noise are debug.
func slogLevel(ent zapcore.Entry) slog.Level {
	n := ent.LoggerName
	switch {
	case hasLoggerPrefix(n, "tls.obtain"), hasLoggerPrefix(n, "tls.renew"),
		hasLoggerPrefix(n, "http.log.error"), hasLoggerPrefix(n, "http.handlers.reverse_proxy"),
		strings.Contains(ent.Message, "in renewal window"):
		return slog.LevelDebug
	case hasLoggerPrefix(n, "tls.issuance") && ent.Level < zapcore.ErrorLevel:
		return slog.LevelDebug
	case hasLoggerPrefix(n, "http.stdlib") && strings.Contains(ent.Message, "TLS handshake error"):
		return slog.LevelDebug
	}
	switch {
	case ent.Level <= zapcore.DebugLevel:
		return slog.LevelDebug
	case ent.Level == zapcore.InfoLevel:
		return slog.LevelInfo
	case ent.Level == zapcore.WarnLevel:
		return slog.LevelWarn
	}
	return slog.LevelError
}

func hasLoggerPrefix(name, prefix string) bool {
	return name == prefix || strings.HasPrefix(name, prefix+".")
}
