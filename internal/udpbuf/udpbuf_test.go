package udpbuf

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func fake(t *testing.T, rmem, wmem int, err error) {
	t.Helper()
	old := Read
	t.Cleanup(func() { Read = old })
	Read = func(name string) (int, error) {
		if err != nil {
			return 0, err
		}
		if name == "rmem_max" {
			return rmem, nil
		}
		return wmem, nil
	}
}

func warnLog(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	Warn(slog.New(slog.NewTextHandler(&buf, nil)))
	return buf.String()
}

func TestLowLimitsWarnOnceNamingTheSysctlsTheValueAndTheGuide(t *testing.T) {
	fake(t, 212992, 212992, nil)
	out := warnLog(t)
	if n := strings.Count(out, "\n"); n != 1 {
		t.Fatalf("want one line, got %d:\n%s", n, out)
	}
	for _, want := range []string{"level=WARN", "net.core.rmem_max", "net.core.wmem_max", "7500000", "install.md#udp-buffers", "rmem_max=212992"} {
		if !strings.Contains(out, want) {
			t.Errorf("line lacks %q:\n%s", want, out)
		}
	}
}

func TestOneLowLimitWarns(t *testing.T) {
	fake(t, 7500000, 212992, nil)
	if warnLog(t) == "" {
		t.Error("a low wmem_max did not warn")
	}
}

func TestLimitsAsDocumentedLogNothing(t *testing.T) {
	fake(t, 7500000, 7500000, nil)
	if out := warnLog(t); out != "" {
		t.Errorf("logged:\n%s", out)
	}
}

func TestLimitsThatCannotBeReadLogNothing(t *testing.T) {
	fake(t, 0, 0, errors.New("no such file"))
	if out := warnLog(t); out != "" {
		t.Errorf("logged:\n%s", out)
	}
}

func TestSilenceQuicGoSetsItsOptOut(t *testing.T) {
	t.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "")
	SilenceQuicGo()
	if os.Getenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING") != "true" {
		t.Error("env not set")
	}
}
