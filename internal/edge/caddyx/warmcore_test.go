package caddyx

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2"
	"go.uber.org/zap/zapcore"

	"github.com/hyprsh/tnl/internal/edge/routes"
	"github.com/hyprsh/tnl/internal/logx"
)

// Caddy builds the certificate cache's logger in the warm load and the full
// load keeps the cache, so the warm load's core outlives the warm load: every
// entry of the cache's logger after it still arrives as a tnl line, while the
// warm load's other loggers, repeated by the full load, stay out.
func TestTheWarmLoadsCoreStillWritesTheCachesEntriesAfterTheWarmLoad(t *testing.T) {
	buf := new(syncBuf)
	rt := &Runtime{Table: new(routes.Mem), Outposts: new(routes.MemOutposts),
		Log: logx.New(logx.Options{Level: slog.LevelDebug, Out: buf})}
	Register("warmcore", rt)
	rt.warming.Store(true)
	l := &LogCore{Edge: "warmcore"}
	if err := l.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	rt.warming.Store(false)

	write := func(name, msg string) {
		ent := zapcore.Entry{LoggerName: name, Message: msg, Level: zapcore.InfoLevel}
		if ce := l.Check(ent, nil); ce != nil {
			ce.Write()
		}
	}
	write("tls", "warm load startup line")
	write("tls.cache", "replaced certificate in cache")
	write("tls.cache.maintenance", "started background certificate maintenance")
	out := strings.Join(buf.lines(), "\n")
	for _, want := range []string{"replaced certificate in cache", "started background certificate maintenance"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "warm load startup line") {
		t.Errorf("the warm load's startup line came through:\n%s", out)
	}
}
