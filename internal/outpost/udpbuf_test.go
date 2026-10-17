package outpost

import (
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/udpbuf/udpbuftest"
)

func logOfAnOutpostOnHostWithLimits(t *testing.T, n int) string {
	t.Helper()
	udpbuftest.Both(t, n)
	tok, edgeKey := newToken(t, "127.0.0.1")
	edge := newFakeEdge(t, edgeKey, 0, tok.OneTimeKey)
	tok.Port = edge.port
	r, err := start(t, Config{Token: tok.Encode(), StateDir: shortStateDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	handshake(t, edge)
	log := r.log.String()
	if err := r.stop(); err != nil {
		t.Fatal(err)
	}
	return log
}

func TestLowUDPBufferLimitsLogOneWarnOnAnOutpostToo(t *testing.T) {
	log := logOfAnOutpostOnHostWithLimits(t, 212992)
	if n := strings.Count(log, "net.core.rmem_max"); n != 1 || !strings.Contains(log, "level=warn") {
		t.Errorf("log:\n%s", log)
	}
}

func TestUDPBufferLimitsAsDocumentedLogNoWarnOnAnOutpost(t *testing.T) {
	if log := logOfAnOutpostOnHostWithLimits(t, 7500000); strings.Contains(log, "net.core") {
		t.Errorf("log:\n%s", log)
	}
}
