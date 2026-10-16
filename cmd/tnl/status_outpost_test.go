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
	relayServing(t, map[string]any{api.PathStatus: s})
}

// relayServing serves a fake Outpost relay that answers GET of each path in
// replies with its value, and points the CLI at it.
func relayServing(t *testing.T, replies map[string]any) {
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
	for path, body := range replies {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) { reply(w, body) })
	}
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

// An Outpost's CLI may be older than the Edge its relay forwards to, and the
// Edge may have added a field to a reply. `route ls`, `zone ls` and `status`
// still print what they know, and --json passes the field on.
func TestAnOlderOutpostReadsAFieldANewerEdgeAdded(t *testing.T) {
	withExtra := func(v any, extra map[string]any, inside ...string) map[string]any {
		b, _ := json.Marshal(v)
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		at := m
		for _, k := range inside {
			if list, ok := at[k].([]any); ok {
				at = list[0].(map[string]any)
			} else {
				at = at[k].(map[string]any)
			}
		}
		for k, x := range extra {
			at[k] = x
		}
		return m
	}
	route := api.RouteRow{RouteInfo: api.RouteInfo{Hostname: "app.example.com", Outpost: "nas", Service: "http://localhost:80"}, Status: api.StatusOK}
	zone := api.ZoneRow{ZoneInfo: api.ZoneInfo{Name: "example.com", Mode: "wildcard"}}
	relayServing(t, map[string]any{
		api.PathRoutes: withExtra(api.RouteList{Routes: []api.RouteRow{route}}, map[string]any{"weight": 3}, "routes"),
		api.PathZones:  withExtra(api.ZoneList{Zones: []api.ZoneRow{zone}}, map[string]any{"tier": "gold"}, "zones"),
		api.PathStatus: withExtra(healthyOutpost(), map[string]any{"also_new": 1}, "edge"),
	})
	for _, c := range []struct {
		args       []string
		want, json string
	}{
		{[]string{"route", "ls"}, "app.example.com", `"weight": 3`},
		{[]string{"zone", "ls"}, "example.com", `"tier": "gold"`},
		{[]string{"status"}, "Outpost nas", `"also_new": 1`},
	} {
		out, code, err := tnl(c.args...)
		if err != nil || code != 0 || !strings.Contains(out, c.want) {
			t.Errorf("%v: %q %v %d", c.args, out, err, code)
		}
		out, _, err = tnl(append(c.args, "--json")...)
		if err != nil || !strings.Contains(out, c.json) {
			t.Errorf("%v --json: %v\n%s", c.args, err, out)
		}
	}
}
