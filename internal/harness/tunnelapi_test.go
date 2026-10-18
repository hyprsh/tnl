package harness

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
)

func TestOutpostReadsZonesAndRoutesThroughTheTunnelAsTheSocketAnswers(t *testing.T) {
	re := StartEdge(t)
	re.AddZone(t, "example.com")
	j := re.JoinOutpost(t, "laptop")
	re.AddRoute(t, "app.example.com", "http://127.0.0.1:3000", "laptop")
	bg := context.Background()

	wantZones, err := api.Call[api.ZoneList](bg, re.API, http.MethodGet, api.PathZones, nil)
	if err != nil {
		t.Fatal(err)
	}
	gotZones, err := api.Call[api.ZoneList](bg, j.API, http.MethodGet, api.PathZones, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotZones, wantZones) || len(gotZones.Zones) != 1 {
		t.Errorf("zones through the Tunnel %v, on the socket %v", gotZones, wantZones)
	}
	wantRoutes, _ := api.Call[api.RouteList](bg, re.API, http.MethodGet, api.PathRoutes, nil)
	gotRoutes, err := api.Call[api.RouteList](bg, j.API, http.MethodGet, api.PathRoutes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotRoutes, wantRoutes) || len(gotRoutes.Routes) != 1 {
		t.Errorf("routes through the Tunnel %v, on the socket %v", gotRoutes, wantRoutes)
	}
}

func TestOutpostIsRefusedZoneAddAndOutpostAddAndTheLogNamesIt(t *testing.T) {
	logs := &LockedBuf{}
	re := StartEdgeWith(t, EdgeOptions{LogOut: logs, LogLevel: slog.LevelWarn})
	j := re.JoinOutpost(t, "laptop")
	bg := context.Background()

	_, err := api.Call[api.ZoneInfo](bg, j.API, http.MethodPost, api.PathZones, api.ZoneAddRequest{Name: "evil.example"})
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Status != http.StatusForbidden || ae.Message == "" {
		t.Errorf("zone add: %v", err)
	}
	_, err = api.Call[api.OutpostAddReply](bg, j.API, http.MethodPost, api.PathOutposts, api.OutpostAddRequest{Name: "evil"})
	if !errors.As(err, &ae) || ae.Status != http.StatusForbidden {
		t.Errorf("outpost add: %v", err)
	}
	if got := logs.String(); !bytes.Contains([]byte(got), []byte("actor=outpost:laptop")) {
		t.Errorf("no log line carries actor=outpost:laptop:\n%s", got)
	}
	st := re.Store.Snapshot()
	if len(st.Zones) != 0 || len(st.Outposts) != 1 {
		t.Errorf("a refused call changed the state")
	}
}

func TestTheTunnelAPIListensOnNoHostAddress(t *testing.T) {
	StartEdge(t)
	c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(api.TunnelPort))
	if err == nil {
		c.Close()
		t.Fatal("something listens on the host's loopback at the API port")
	}
}
