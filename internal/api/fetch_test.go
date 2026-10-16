package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

// serve answers GET path with body, and PathInfo as an Edge, on a tnl.sock,
// and returns a Client for it.
func serve(t *testing.T, path, body string) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "tnl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ln, err := ListenSocket(dir)
	if err != nil {
		t.Fatal(err)
	}
	const v = "1.0.0"
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, s string) {
		w.Header().Set(VersionHeader, v)
		w.Write([]byte(s))
	}
	mux.HandleFunc("GET "+PathInfo, func(w http.ResponseWriter, _ *http.Request) {
		b, _ := json.Marshal(Info{Role: RoleEdge, Version: v})
		write(w, string(b))
	})
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, _ *http.Request) { write(w, body) })
	hs := &http.Server{Handler: mux}
	go hs.Serve(ln)
	t.Cleanup(func() { hs.Close() })
	return &Client{StateDir: dir, Version: v}
}

// A newer Edge may add a field to any reply. An older tnl still reads the rest
// of an `ls` or status reply and keeps the bytes for --json, where Call, which
// refuses what it doesn't know, would fail.
func TestFetchReadsAReplyWithFieldsItDoesNotKnow(t *testing.T) {
	body := `{"zones":[{"name":"example.com","mode":"per-hostname","from_the_future":1}],"also":true}`
	c := serve(t, PathZones, body)
	ctx := context.Background()
	if _, err := Call[ZoneList](ctx, c, http.MethodGet, PathZones, nil); err == nil {
		t.Fatal("Call read a field it doesn't know")
	}
	list, raw, err := Fetch[ZoneList](ctx, c, PathZones)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Zones) != 1 || list.Zones[0].Name != "example.com" {
		t.Errorf("%+v", list)
	}
	if !strings.Contains(string(raw), "from_the_future") || !strings.Contains(string(raw), "also") {
		t.Errorf("raw %s", raw)
	}
}
