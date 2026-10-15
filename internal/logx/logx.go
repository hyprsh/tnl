// Package logx is the logging base both roles share: log/slog to stderr as
// key=value text or JSON, journald-aware, with the four levels the spec names.
package logx

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"golang.zx2c4.com/wireguard/device"
)

// Options configure New.
type Options struct {
	Level   slog.Level
	JSON    bool
	Journal bool // drop tnl's timestamp, prefix each line with <N>
	Out     io.Writer
}

// New builds a logger from resolved options.
func New(o Options) *slog.Logger {
	lvl := new(slog.LevelVar)
	lvl.Set(o.Level)
	ho := &slog.HandlerOptions{Level: lvl, ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
		if len(groups) == 0 {
			switch a.Key {
			case slog.TimeKey:
				if o.Journal {
					return slog.Attr{} // journald stamps lines itself
				}
			case slog.LevelKey:
				a.Value = slog.StringValue(strings.ToLower(a.Value.String()))
			}
		}
		return a
	}}
	mk := func(w io.Writer) slog.Handler {
		if o.JSON {
			return slog.NewJSONHandler(w, ho)
		}
		return slog.NewTextHandler(w, ho)
	}
	if !o.Journal {
		return slog.New(mk(o.Out))
	}
	return slog.New(&journalHandler{mk: mk, lvl: lvl, out: o.Out, mu: &sync.Mutex{}})
}

// journalHandler renders each record with an inner handler, then writes the
// syslog priority prefix and the line in one Write.
type journalHandler struct {
	mk  func(io.Writer) slog.Handler
	lvl slog.Leveler
	out io.Writer
	mu  *sync.Mutex
	ops []func(slog.Handler) slog.Handler // WithAttrs/WithGroup calls, replayed per record
}

func (h *journalHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.lvl.Level() }

func (h *journalHandler) Handle(ctx context.Context, r slog.Record) error {
	var buf bytes.Buffer
	buf.WriteString(priority(r.Level))
	inner := h.mk(&buf)
	for _, op := range h.ops {
		inner = op(inner)
	}
	if err := inner.Handle(ctx, r); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.out.Write(buf.Bytes())
	return err
}

func (h *journalHandler) with(op func(slog.Handler) slog.Handler) slog.Handler {
	c := *h
	c.ops = append(append([]func(slog.Handler) slog.Handler(nil), h.ops...), op)
	return &c
}

func (h *journalHandler) WithAttrs(as []slog.Attr) slog.Handler {
	if len(as) == 0 {
		return h
	}
	return h.with(func(i slog.Handler) slog.Handler { return i.WithAttrs(as) })
}

func (h *journalHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return h.with(func(i slog.Handler) slog.Handler { return i.WithGroup(name) })
}

// priority is the sd-daemon(3) prefix journald reads.
func priority(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "<3>"
	case l >= slog.LevelWarn:
		return "<4>"
	case l >= slog.LevelInfo:
		return "<6>"
	default:
		return "<7>"
	}
}

// Config holds the --log-level and --log-format settings.
type Config struct {
	Level  string
	Format string
}

// EnvVars are the variables Config reads, for the unknown-TNL_* check.
var EnvVars = []string{"TNL_LOG_LEVEL", "TNL_LOG_FORMAT"}

// AddFlags registers --log-level and --log-format on fs, defaulting to
// TNL_LOG_LEVEL and TNL_LOG_FORMAT, then to info and text.
func AddFlags(fs *flag.FlagSet) *Config {
	c := &Config{}
	fs.StringVar(&c.Level, "log-level", envOr("TNL_LOG_LEVEL", "info"), "log level: error, warn, info or debug (TNL_LOG_LEVEL)")
	fs.StringVar(&c.Format, "log-format", envOr("TNL_LOG_FORMAT", "text"), "log format: text or json (TNL_LOG_FORMAT)")
	return c
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// Logger builds the logger for out. journalStream is $JOURNAL_STREAM; when out
// is the file it names, lines are journal-style. It does not touch
// slog.SetDefault: the caller does that, so Go's log package follows.
func (c *Config) Logger(out io.Writer, journalStream string) (*slog.Logger, error) {
	var lvl slog.Level
	switch c.Level {
	case "error":
		lvl = slog.LevelError
	case "warn":
		lvl = slog.LevelWarn
	case "info":
		lvl = slog.LevelInfo
	case "debug":
		lvl = slog.LevelDebug
	default:
		return nil, fmt.Errorf("--log-level %q: want error, warn, info or debug", c.Level)
	}
	if c.Format != "text" && c.Format != "json" {
		return nil, fmt.Errorf("--log-format %q: want text or json", c.Format)
	}
	f, isFile := out.(*os.File)
	return New(Options{
		Level:   lvl,
		JSON:    c.Format == "json",
		Journal: isFile && OnJournal(f, journalStream),
		Out:     out,
	}), nil
}

// WireGuardLogger routes wireguard-go's Verbosef and Errorf to debug. Above
// debug it uses DiscardLogf, since wireguard-go panics on a nil function.
func WireGuardLogger(lg *slog.Logger) *device.Logger {
	if !lg.Enabled(context.Background(), slog.LevelDebug) {
		return &device.Logger{Verbosef: device.DiscardLogf, Errorf: device.DiscardLogf}
	}
	dbg := func(f string, a ...any) {
		lg.Debug(strings.TrimSuffix(fmt.Sprintf(f, a...), "\n"), "component", "wireguard")
	}
	return &device.Logger{Verbosef: dbg, Errorf: dbg}
}

// LoggedError wraps an error that was already logged at error, so the caller
// only needs to exit.
type LoggedError struct{ Err error }

func (e *LoggedError) Error() string { return e.Err.Error() }
func (e *LoggedError) Unwrap() error { return e.Err }
