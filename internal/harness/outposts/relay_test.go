package outposts

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"

	. "github.com/hyprsh/tnl/internal/harness"
)

func refusal(t *testing.T, err error, status int, want ...string) {
	t.Helper()
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Status != status {
		t.Fatalf("got %v, want a %d refusal", err, status)
	}
	for _, w := range want {
		if !strings.Contains(ae.Message, w) {
			t.Fatalf("refusal %q does not contain %q", ae.Message, w)
		}
	}
}

func TestOutpostAddsARouteThroughItselfAndTheEdgeServesItAndLogsTheActor(t *testing.T) {
	svc := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hello")) }))
	var edgeLog LockedBuf
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{}), LogOut: &edgeLog})
	re.AddZone(t, "test")
	var outLog LockedBuf
	tok := re.AddOutpost(t, "nas")
	nas, err := re.TryConnectWith(t, "nas", tok, MustTempDir(t), ConnectOptions{LogOut: &outLog})
	if err != nil {
		t.Fatal(err)
	}

	// The Outpost's CLI asks the relay for its role, and the Edge fills in the caller.
	info, err := api.Call[api.Info](Ctx(t), nas.CLI, http.MethodGet, api.PathInfo, nil)
	if err != nil || info.Role != api.RoleOutpost {
		t.Fatalf("info: %+v %v", info, err)
	}
	_, err = api.Call[api.RouteInfo](Ctx(t), nas.CLI, http.MethodPost, api.PathRoutes,
		api.RouteAddRequest{Hostname: "a.test", Service: "http://" + svc.Addr()})
	if err != nil {
		t.Fatal(err)
	}
	if resp, body := Get(t, re.Caddy, H1, "a.test", "/"); resp.StatusCode != 200 || body != "hello" {
		t.Fatalf("Route served %d %q", resp.StatusCode, body)
	}
	if !strings.Contains(edgeLog.String(), "actor=outpost:nas") {
		t.Fatalf("the Edge's log lacks actor=outpost:nas:\n%s", edgeLog.String())
	}
	if !strings.Contains(outLog.String(), "a.test") || !strings.Contains(outLog.String(), "/v1/routes") {
		t.Fatalf("the Outpost did not log the Route change:\n%s", outLog.String())
	}

	// Edit and remove its own Route; ls shows the same as the Edge's.
	if _, err := api.Call[api.RouteInfo](Ctx(t), nas.CLI, http.MethodPost, api.PathRouteEdit,
		api.RouteEditRequest{Hostname: "a.test", HostHeader: ptr("x.lan")}); err != nil {
		t.Fatal(err)
	}
	mine, _ := api.Call[api.RouteList](Ctx(t), nas.CLI, http.MethodGet, api.PathRoutes, nil)
	edges, _ := api.Call[api.RouteList](Ctx(t), re.API, http.MethodGet, api.PathRoutes, nil)
	if len(mine.Routes) != 1 || len(edges.Routes) != 1 || mine.Routes[0].Hostname != edges.Routes[0].Hostname || strings.Join(mine.Routes[0].Options, " ") != "host-header=x.lan" {
		t.Fatalf("ls: %+v vs %+v", mine, edges)
	}
	zs, err := api.Call[api.ZoneList](Ctx(t), nas.CLI, http.MethodGet, api.PathZones, nil)
	if err != nil || len(zs.Zones) != 1 || zs.Zones[0].Name != "test" {
		t.Fatalf("zone ls: %+v %v", zs, err)
	}
	if _, err := api.Call[api.RouteRmReply](Ctx(t), nas.CLI, http.MethodPost, api.PathRouteRm, api.RouteRmRequest{Hostname: "a.test"}); err != nil {
		t.Fatal(err)
	}
}

func TestAnOutpostCannotTouchAnotherOutpostsRoutesOrHostnames(t *testing.T) {
	re := StartEdge(t)
	re.AddZone(t, "test")
	nas := re.JoinOutpost(t, "nas")
	re.JoinOutpost(t, "laptop")
	re.AddRoute(t, "nas.test", "http://127.0.0.1:1", "nas")
	re.AddRoute(t, "lap.test", "http://127.0.0.1:1", "laptop")

	call := func(path string, req any) error {
		_, err := api.Call[api.RouteInfo](Ctx(t), nas.CLI, http.MethodPost, path, req)
		return err
	}
	// A held Hostname is not taken, and the refusal names its holder.
	refusal(t, call(api.PathRoutes, api.RouteAddRequest{Hostname: "lap.test", Service: "http://127.0.0.1:2"}), 409, "laptop")
	// Another Outpost's Route is not edited or removed.
	refusal(t, call(api.PathRouteEdit, api.RouteEditRequest{Hostname: "lap.test", HostHeader: ptr("x")}), 403, "laptop")
	refusal(t, call(api.PathRouteRm, api.RouteRmRequest{Hostname: "lap.test"}), 403, "laptop")
	// Its own Route does not move onto another Outpost, and adding names no other Outpost.
	refusal(t, call(api.PathRouteEdit, api.RouteEditRequest{Hostname: "nas.test", Outpost: ptr("laptop")}), 403, "laptop")
	refusal(t, call(api.PathRoutes, api.RouteAddRequest{Hostname: "new.test", Service: "http://127.0.0.1:2", Outpost: "laptop"}), 403, "laptop")

	list, _ := api.Call[api.RouteList](Ctx(t), re.API, http.MethodGet, api.PathRoutes, nil)
	if len(list.Routes) != 2 || list.Routes[0].Outpost != "laptop" || list.Routes[1].Outpost != "nas" ||
		list.Routes[0].Options != nil || list.Routes[1].Options != nil {
		t.Fatalf("Routes changed: %+v", list.Routes)
	}
}

func TestRouteCommandsOnAnOutpostSayNotConnectedWhenTheEdgeIsDown(t *testing.T) {
	re := StartEdge(t)
	re.AddZone(t, "test")
	nas := re.JoinOutpost(t, "nas")
	re.Stop()
	start := time.Now()
	_, err := api.Call[api.RouteList](Ctx(t), nas.CLI, http.MethodGet, api.PathRoutes, nil)
	if err == nil || !strings.Contains(err.Error(), "Outpost not connected") {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
}

func TestACLIOfAnotherVersionIsRefusedBeforeAnythingIsSent(t *testing.T) {
	re := StartEdge(t)
	re.AddZone(t, "test")
	nas := re.JoinOutpost(t, "nas")
	old := &api.Client{StateDir: nas.StateDir, Version: "v0.0.1"}
	_, err := api.Call[api.RouteInfo](Ctx(t), old, http.MethodPost, api.PathRoutes, api.RouteAddRequest{Hostname: "a.test", Service: "http://127.0.0.1:2"})
	want := "this `tnl` is v0.0.1 but the running Outpost is " + HarnessVersion + "; restart it (`sudo systemctl restart tnl-outpost`)"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v, want %q", err, want)
	}
	if list, _ := api.Call[api.RouteList](Ctx(t), re.API, http.MethodGet, api.PathRoutes, nil); len(list.Routes) != 0 {
		t.Fatalf("a Route was added: %+v", list)
	}
}
