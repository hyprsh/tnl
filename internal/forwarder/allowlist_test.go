package forwarder

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"syscall"
	"testing"

	"github.com/hyprsh/tnl/internal/allow"
	"github.com/hyprsh/tnl/internal/wire"
)

// handle runs one tnl exchange against f and returns the reply.
func handle(t *testing.T, f *Forwarder, h wire.Header) wire.Reply {
	t.Helper()
	a, b := net.Pipe()
	defer a.Close()
	go f.Handle(b)
	if err := wire.WriteHeader(a, h); err != nil {
		t.Fatal(err)
	}
	rp, err := wire.ReadReply(a)
	if err != nil {
		t.Fatal(err)
	}
	return rp
}

func TestAllowlistRefusesWithoutDialingAndLogsTheRequestID(t *testing.T) {
	list, err := allow.Parse("192.168.1.0/24,localhost:3000")
	if err != nil {
		t.Fatal(err)
	}
	var dialed []string
	var buf bytes.Buffer
	f := &Forwarder{
		Allow: list, Log: slog.New(slog.NewTextHandler(&buf, nil)),
		Dialer: func(_ context.Context, _, addr string) (net.Conn, error) {
			dialed = append(dialed, addr)
			return nil, syscall.ECONNREFUSED
		},
	}
	rp := handle(t, f, wire.Header{Service: "10.1.2.3:80", Hostname: "app.test", RequestID: "req-123"})
	if rp.Status != wire.StatusDialError || rp.Reason != wire.ReasonAllowlist {
		t.Fatalf("got %+v", rp)
	}
	if len(dialed) != 0 {
		t.Fatalf("dialed %v", dialed)
	}
	for _, want := range []string{"level=INFO", "req-123", "10.1.2.3:80"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("log lacks %q: %s", want, buf.String())
		}
	}

	// Listed Services are dialed, a CIDR match at the address it matched.
	handle(t, f, wire.Header{Service: "localhost:3000", Hostname: "app.test", RequestID: "r2"})
	handle(t, f, wire.Header{Service: "192.168.1.7:9", Hostname: "app.test", RequestID: "r3"})
	if fmt.Sprint(dialed) != "[localhost:3000 192.168.1.7:9]" {
		t.Fatalf("dialed %v", dialed)
	}
}
