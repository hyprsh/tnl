package edge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/udpbuf"
)

func fakeUDPLimits(t *testing.T, n int) {
	t.Helper()
	old := udpbuf.Read
	t.Cleanup(func() { udpbuf.Read = old })
	udpbuf.Read = func(string) (int, error) { return n, nil }
}

func TestLowUDPBufferLimitsLogOneWarnNamingTheSysctlsAtStart(t *testing.T) {
	fakeUDPLimits(t, 212992)
	buf, err := run(t, filepath.Join(shortDir(t), "state"))
	if err != nil {
		t.Fatal(err)
	}
	log := buf.String()
	if n := strings.Count(log, "net.core.rmem_max"); n != 1 {
		t.Errorf("%d lines name the sysctl:\n%s", n, log)
	}
	if !strings.Contains(log, "level=warn") || !strings.Contains(log, "7500000") {
		t.Errorf("log:\n%s", log)
	}
	if os.Getenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING") != "true" {
		t.Error("quic-go's own line is not silenced")
	}
}

func TestUDPBufferLimitsAsDocumentedLogNoWarn(t *testing.T) {
	fakeUDPLimits(t, 7500000)
	buf, err := run(t, filepath.Join(shortDir(t), "state"))
	if err != nil {
		t.Fatal(err)
	}
	if log := buf.String(); strings.Contains(log, "net.core") {
		t.Errorf("log:\n%s", log)
	}
}
