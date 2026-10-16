package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
)

// relayAnswering serves a fake Outpost relay that answers status with s, and
// points the CLI at it. It runs in both builds.
func relayAnswering(t *testing.T, s api.OutpostStatus) {
	t.Helper()
	dir, err := os.MkdirTemp("", "tnl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ln, err := api.ListenSocket(dir)
	if err != nil {
		t.Fatal(err)
	}
	v, rev := versionInfo()
	reply := func(w http.ResponseWriter, body any) {
		w.Header().Set(api.VersionHeader, v)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(body)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+api.PathInfo, func(w http.ResponseWriter, r *http.Request) {
		reply(w, api.Info{Role: api.RoleOutpost, Version: v, Build: buildName, Revision: rev})
	})
	mux.HandleFunc("GET "+api.PathStatus, func(w http.ResponseWriter, r *http.Request) { reply(w, s) })
	hs := &http.Server{Handler: mux}
	go hs.Serve(ln)
	t.Cleanup(func() { hs.Close() })
	t.Setenv("TNL_STATE_DIR", dir)
}

func healthyOutpost() api.OutpostStatus {
	return api.OutpostStatus{
		Role: api.RoleOutpost, Name: "nas", Version: "1.2.0", Build: "outpost", TunnelAddress: "100.64.0.2", StateDir: "/var/lib/tnl",
		Edge:      api.OutpostEdge{Address: "edge.example.com", IP: "203.0.113.7", State: api.TunnelUp, Version: "1.2.0"},
		Routes:    []api.OutpostRoute{},
		Attention: []api.AttentionItem{},
	}
}

func TestStatusOfAHealthyOutpostExitsZero(t *testing.T) {
	relayAnswering(t, healthyOutpost())
	out, code, err := tnl("status")
	if err != nil || code != 0 {
		t.Fatalf("%q %v %d", out, err, code)
	}
	for _, want := range []string{"Outpost nas, tnl 1.2.0", "edge.example.com (203.0.113.7)", "Nothing needs attention"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
}

func TestStatusOfAnOutpostWithAProblemExitsOneQuietly(t *testing.T) {
	s := healthyOutpost()
	s.Edge.State, s.Edge.SilentSeconds, s.Routes, s.RoutesUnavailable = api.TunnelDown, 240, nil, "unavailable while the Tunnel is down"
	s.Attention = []api.AttentionItem{{Kind: api.AttentionTunnelDown, Message: "no answer from the Edge for 4m"}}
	relayAnswering(t, s)
	out, code, err := tnl("status")
	if code != 1 || !silent(err) {
		t.Fatalf("code %d, err %v", code, err)
	}
	for _, want := range []string{"no answer for 4m", "Routes: unavailable while the Tunnel is down", "Needs attention\n  - no answer from the Edge for 4m\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
}

func TestStatusOfAnOutpostJSONIsTheReplyAsItCame(t *testing.T) {
	relayAnswering(t, healthyOutpost())
	out, code, err := tnl("status", "--json")
	if err != nil || code != 0 {
		t.Fatalf("%q %v %d", out, err, code)
	}
	var s api.OutpostStatus
	if err := json.Unmarshal([]byte(out), &s); err != nil || s.Role != api.RoleOutpost || s.Attention == nil || s.Routes == nil {
		t.Fatalf("%v %+v\n%s", err, s, out)
	}
}
