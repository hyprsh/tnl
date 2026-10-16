package harness

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/render"
)

// rows prints a table the way `tnl ... ls` does and returns its lines with the
// runs of spaces squeezed to one, header first.
func rows(t *testing.T, print func(*bytes.Buffer) error) []string {
	t.Helper()
	var b bytes.Buffer
	if err := print(&b); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		out = append(out, strings.Join(strings.Fields(l), " "))
	}
	return out
}

func outpostRows(t *testing.T, c *api.Client) []string {
	t.Helper()
	l, err := api.Call[api.OutpostList](ctx(t), c, http.MethodGet, api.PathOutposts, nil)
	if err != nil {
		t.Fatal(err)
	}
	return rows(t, func(b *bytes.Buffer) error { return render.Outposts(b, l) })
}

func rowOf(t *testing.T, lines []string, prefix string) string {
	t.Helper()
	for _, l := range lines {
		if strings.HasPrefix(l, prefix+" ") {
			return l
		}
	}
	t.Fatalf("no row for %s in\n%s", prefix, strings.Join(lines, "\n"))
	return ""
}

// A real Edge with a real Outpost in each state prints each state in the
// spec's forms.
func TestOutpostLsShowsOnlineOfflineIncompatibleAndNotJoined(t *testing.T) {
	re := StartEdgeWith(t, EdgeOptions{Version: "0.3.4", OfflineAfter: testOfflineAfter})
	opts := func(v string) ConnectOptions { return ConnectOptions{JoinTimeout: 3 * time.Second, Version: v} }
	connect := func(name, v string) *JoinedOutpost {
		j, err := re.TryConnectWith(t, name, re.AddOutpost(t, name), mustTempDir(t), opts(v))
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	connect("up", "0.3.4")
	gone := connect("gone", "0.3.4")
	old := connect("old", "0.3.1")
	re.AddOutpost(t, "newbox")
	reconnectAs(t, re, "old", old, "0.4.0")
	gone.Stop()
	waitFor(t, "gone to be Offline", 5*testOfflineAfter, func() bool { return re.Liveness.State("gone") == liveness.Offline })
	waitFor(t, "old to be Incompatible", 5*time.Second, func() bool { return re.Liveness.State("old") == liveness.Incompatible })
	waitFor(t, "up's version", 5*time.Second, func() bool { return re.Liveness.Info("up").Version == "0.3.4" })

	lines := outpostRows(t, re.API)
	if lines[0] != "NAME STATE LAST SEEN ENDPOINT VERSION TUNNEL ADDRESS ROUTES" {
		t.Fatalf("header %q", lines[0])
	}
	var names []string
	for _, l := range lines[1:] {
		names = append(names, strings.Fields(l)[0])
	}
	if strings.Join(names, ",") != "gone,newbox,old,up" {
		t.Fatalf("rows are not sorted by name: %v", names)
	}
	up := strings.Fields(rowOf(t, lines, "up"))
	if up[1] != "online" || !strings.HasSuffix(up[2], "s") || up[3] != "ago" || up[4] == "–" || up[5] != "0.3.4" || up[7] != "0" {
		t.Errorf("up: %v", up)
	}
	if f := strings.Fields(rowOf(t, lines, "gone")); f[1] != "offline" || f[3] != "ago" {
		t.Errorf("gone: %v", f)
	}
	if f := strings.Fields(rowOf(t, lines, "old")); f[1] != "incompatible" || f[5] != "0.4.0" {
		t.Errorf("old: %v", f)
	}
	nb := rowOf(t, lines, "newbox")
	if !strings.HasPrefix(nb, "newbox not joined token expires in ") || !strings.HasSuffix(nb, "m – – 100.64.0.5 0") {
		t.Errorf("newbox: %q", nb)
	}
}

// Right after an Edge restart, an Outpost not yet heard from is online with
// nothing seen; once it pings, its time, endpoint and version fill in.
func TestOutpostLsAfterARestartShowsDashesUntilTheOutpostIsHeard(t *testing.T) {
	re := StartEdge(t)
	j := re.JoinOutpost(t, "nas")
	if err := j.Stop(); err != nil {
		t.Fatal(err)
	}
	re.Stop()

	again := StartEdgeWith(t, EdgeOptions{StateDir: re.StateDir})
	got := rowOf(t, outpostRows(t, again.API), "nas")
	if f := strings.Fields(got); f[1] != "online" || f[2] != "–" || f[3] != "–" || f[4] != "–" {
		t.Fatalf("right after the restart: %q", got)
	}
}

func TestRouteLsAndZoneLsOnAnOutpostPrintWhatTheEdgePrints(t *testing.T) {
	svc := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	re := StartEdge(t)
	re.AddZone(t, "test")
	nas := re.JoinOutpost(t, "nas")
	re.AddRoute(t, photos, "http://"+svc.Addr(), "nas")
	re.ConnectWith(t, "newbox", re.AddOutpost(t, "newbox"),
		ConnectOptions{Declaration: []outpostLine{DeclaredLine("PHOTOS", photos, "http://"+svc.Addr())}})

	printed := func(c *api.Client) (routes, zones []string) {
		rl, err := api.Call[api.RouteList](ctx(t), c, http.MethodGet, api.PathRoutes, nil)
		if err != nil {
			t.Fatal(err)
		}
		zl, err := api.Call[api.ZoneList](ctx(t), c, http.MethodGet, api.PathZones, nil)
		if err != nil {
			t.Fatal(err)
		}
		return rows(t, func(b *bytes.Buffer) error { return render.Routes(b, rl) }),
			rows(t, func(b *bytes.Buffer) error { return render.Zones(b, zl) })
	}
	waitFor(t, "the line to wait", 5*time.Second, func() bool {
		r, _ := printed(re.API)
		return len(r) == 3
	})
	er, ez := printed(re.API)
	or, oz := printed(nas.CLI)
	if strings.Join(er, "\n") != strings.Join(or, "\n") || strings.Join(ez, "\n") != strings.Join(oz, "\n") {
		t.Fatalf("on the Outpost:\n%s\n%s\non the Edge:\n%s\n%s", strings.Join(or, "\n"), strings.Join(oz, "\n"), strings.Join(er, "\n"), strings.Join(ez, "\n"))
	}
	// A move: one Hostname, two rows, the Route first.
	if er[1] != photos+" nas http://"+svc.Addr()+" – ok" {
		t.Errorf("the Route row: %q", er[1])
	}
	if !strings.HasPrefix(er[2], photos+" newbox ") || !strings.HasSuffix(er[2], "waiting: taken by nas") {
		t.Errorf("the waiting row: %q", er[2])
	}
	if ez[1] != "test per-hostname – 1 certificate" {
		t.Errorf("the Zone row: %q", ez[1])
	}
}
