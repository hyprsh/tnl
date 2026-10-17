package udpbuf

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func measured(recv, send int, err error) Measure {
	return func() (int, int, error) { return recv, send, err }
}

func warnLog(m Measure) string {
	var buf bytes.Buffer
	Warn(slog.New(slog.NewTextHandler(&buf, nil)), m)
	return buf.String()
}

func TestSmallBuffersWarnOnceNamingTheSysctlsTheSizesAndTheGuide(t *testing.T) {
	out := warnLog(measured(425984, 425984, nil))
	if n := strings.Count(out, "\n"); n != 1 {
		t.Fatalf("want one line, got %d:\n%s", n, out)
	}
	for _, want := range []string{"level=WARN", "net.core.rmem_max", "net.core.wmem_max", "to 7500000", "wanted=7340032", "install.md#udp-buffers", "recv_buffer=425984", "send_buffer=425984"} {
		if !strings.Contains(out, want) {
			t.Errorf("line lacks %q:\n%s", want, out)
		}
	}
}

func TestOneSmallBufferWarns(t *testing.T) {
	if warnLog(measured(14680064, 425984, nil)) == "" {
		t.Error("a small send buffer did not warn")
	}
}

func TestBuffersAsWantedLogNothing(t *testing.T) {
	if out := warnLog(measured(14680064, 14680064, nil)); out != "" {
		t.Errorf("logged:\n%s", out)
	}
}

func TestBuffersThatCannotBeMeasuredLogNothing(t *testing.T) {
	if out := warnLog(measured(0, 0, errors.New("no socket"))); out != "" {
		t.Errorf("logged:\n%s", out)
	}
}

func TestNilMeasureMeasuresARealSocket(t *testing.T) {
	// Whatever the host grants, Warn must run without a fake and log at most one line.
	if n := strings.Count(warnLog(nil), "\n"); n > 1 {
		t.Errorf("%d lines", n)
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
