package udpbuf

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func fake(t *testing.T, recv, send int, err error) {
	t.Helper()
	old := Granted
	t.Cleanup(func() { Granted = old })
	Granted = func() (int, int, error) { return recv, send, err }
}

func warnLog(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	Warn(slog.New(slog.NewTextHandler(&buf, nil)))
	return buf.String()
}

func TestSmallBuffersWarnOnceNamingTheSysctlsTheSizesAndTheGuide(t *testing.T) {
	fake(t, 425984, 425984, nil)
	out := warnLog(t)
	if n := strings.Count(out, "\n"); n != 1 {
		t.Fatalf("want one line, got %d:\n%s", n, out)
	}
	for _, want := range []string{"level=WARN", "net.core.rmem_max", "net.core.wmem_max", "7500000", "install.md#udp-buffers", "recv_buffer=425984", "send_buffer=425984"} {
		if !strings.Contains(out, want) {
			t.Errorf("line lacks %q:\n%s", want, out)
		}
	}
}

func TestOneSmallBufferWarns(t *testing.T) {
	fake(t, 14680064, 425984, nil)
	if warnLog(t) == "" {
		t.Error("a small send buffer did not warn")
	}
}

func TestBuffersAsWantedLogNothing(t *testing.T) {
	fake(t, 14680064, 14680064, nil)
	if out := warnLog(t); out != "" {
		t.Errorf("logged:\n%s", out)
	}
}

func TestBuffersThatCannotBeMeasuredLogNothing(t *testing.T) {
	fake(t, 0, 0, errors.New("no socket"))
	if out := warnLog(t); out != "" {
		t.Errorf("logged:\n%s", out)
	}
}

func TestGrantedMeasuresARealSocket(t *testing.T) {
	recv, send, err := Granted()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("granted recv=%d send=%d", recv, send)
	if recv <= 0 || send <= 0 {
		t.Errorf("recv=%d send=%d", recv, send)
	}
}

func TestSilenceQuicGoSetsItsOptOut(t *testing.T) {
	t.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "")
	if err := SilenceQuicGo(); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING") != "true" {
		t.Error("env not set")
	}
}
