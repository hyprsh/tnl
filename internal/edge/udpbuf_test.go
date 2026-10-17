package edge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func grantedBoth(n int) func() (int, int, error) {
	return func() (int, int, error) { return n, n, nil }
}

func TestSmallGrantedUDPBuffersLogOneWarnNamingTheSysctlsAtStart(t *testing.T) {
	buf, err := runWith(t, Config{StateDir: filepath.Join(shortDir(t), "state"), UDPBuffers: grantedBoth(212992)})
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

func TestUDPBuffersGrantedAsDocumentedLogNoWarn(t *testing.T) {
	buf, err := runWith(t, Config{StateDir: filepath.Join(shortDir(t), "state"), UDPBuffers: grantedBoth(7500000)})
	if err != nil {
		t.Fatal(err)
	}
	if log := buf.String(); strings.Contains(log, "net.core") {
		t.Errorf("log:\n%s", log)
	}
}
