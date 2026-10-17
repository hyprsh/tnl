package outpost

import (
	"strings"
	"testing"
)

func logOfAnOutpostGrantedUDPBuffers(t *testing.T, n int) string {
	t.Helper()
	tok, edgeKey := newToken(t, "127.0.0.1")
	edge := newFakeEdge(t, edgeKey, 0, tok.OneTimeKey)
	tok.Port = edge.port
	granted := func() (int, int, error) { return n, n, nil }
	r, err := start(t, Config{Token: tok.Encode(), StateDir: shortStateDir(t), UDPBuffers: granted})
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

func TestSmallGrantedUDPBuffersLogOneWarnOnAnOutpostToo(t *testing.T) {
	log := logOfAnOutpostGrantedUDPBuffers(t, 212992)
	if n := strings.Count(log, "net.core.rmem_max"); n != 1 || !strings.Contains(log, "level=warn") {
		t.Errorf("log:\n%s", log)
	}
}

func TestUDPBuffersGrantedAsDocumentedLogNoWarnOnAnOutpost(t *testing.T) {
	if log := logOfAnOutpostGrantedUDPBuffers(t, 7500000); strings.Contains(log, "net.core") {
		t.Errorf("log:\n%s", log)
	}
}
