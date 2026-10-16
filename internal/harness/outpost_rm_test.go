package harness

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
)

func outpostRm(t *testing.T, re *RealEdge, name string, withRoutes bool) (api.OutpostRmResponse, error) {
	return api.Call[api.OutpostRmResponse](ctx(t), re.API, http.MethodPost, api.PathOutpostRm, api.OutpostRmRequest{Name: name, WithRoutes: withRoutes})
}

func outpostRekey(t *testing.T, re *RealEdge, name string) (api.OutpostAddResponse, error) {
	return api.Call[api.OutpostAddResponse](ctx(t), re.API, http.MethodPost, api.PathOutpostRekey, api.OutpostRekeyRequest{Name: name})
}

func TestOutpostRmRefusesWhileRoutesPointAtItAndChangesNothing(t *testing.T) {
	re := StartEdge(t)
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, "a.test", "http://localhost:1", "nas")
	re.AddRoute(t, "b.test", "http://localhost:1", "nas")
	_, err := outpostRm(t, re, "nas", false)
	e, ok := err.(*api.Error)
	if !ok || e.Status != http.StatusConflict || !strings.Contains(e.Message, "a.test") || !strings.Contains(e.Message, "b.test") {
		t.Fatalf("err = %v", err)
	}
	st := re.Store.Snapshot()
	if len(st.Outposts) != 1 || len(st.Routes) != 2 || len(edgePeers(t, re)) != 1 {
		t.Fatalf("the refusal changed something: %+v, peers %v", st, edgePeers(t, re))
	}
}

func TestOutpostRmWithRoutesRemovesItsRoutesPeerAndStreams(t *testing.T) {
	svc := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		rc := http.NewResponseController(w)
		for {
			if _, err := fmt.Fprint(w, "data: x\n\n"); err != nil || rc.Flush() != nil {
				return
			}
			select {
			case <-time.After(20 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
	}))
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	re.JoinOutpost(t, "other")
	re.AddRoute(t, "a.test", "http://"+svc.Addr(), "nas")
	re.AddRoute(t, "b.test", "http://"+svc.Addr(), "other")
	next := openStream(t, re, "a.test")
	nextB := openStream(t, re, "b.test")
	if !next() || !nextB() {
		t.Fatal("a stream did not start")
	}
	resp, err := outpostRm(t, re, "nas", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Routes) != 1 || resp.Routes[0] != "a.test" {
		t.Fatalf("removed Routes %v", resp.Routes)
	}
	ended := false
	for i := 0; i < 1000 && !ended; i++ {
		ended = !next()
	}
	if !ended {
		t.Fatal("the stream is still flowing after outpost rm")
	}
	if !nextB() {
		t.Fatal("the other Outpost's stream stopped")
	}
	st := re.Store.Snapshot()
	if len(st.Outposts) != 1 || st.Outposts[0].Name != "other" || len(st.Routes) != 1 || len(edgePeers(t, re)) != 1 {
		t.Fatalf("state %+v, peers %v", st, edgePeers(t, re))
	}
	if r, _ := get(t, re.Caddy, H1, "a.test", "/x"); r.StatusCode != 404 {
		t.Fatalf("a.test answered %d", r.StatusCode)
	}
}

func TestOutpostRmCancelsTheTokenOfOneThatHasNotJoined(t *testing.T) {
	re := StartEdge(t)
	tok := re.AddOutpost(t, "laptop")
	if _, err := outpostRm(t, re, "laptop", false); err != nil {
		t.Fatal(err)
	}
	if len(edgePeers(t, re)) != 0 {
		t.Fatalf("peers %v", edgePeers(t, re))
	}
	if _, err := re.TryConnectWith(t, "laptop", tok, mustTempDir(t), ConnectOptions{JoinTimeout: 2 * time.Second}); err == nil {
		t.Fatal("the cancelled token joined")
	}
}

func TestAnOutpostCannotRemoveOrRekeyAnyone(t *testing.T) {
	re := StartEdge(t)
	j := re.JoinOutpost(t, "laptop")
	re.AddOutpost(t, "nas")
	for _, name := range []string{"laptop", "nas"} {
		_, err := api.Call[api.OutpostRmResponse](ctx(t), j.API, http.MethodPost, api.PathOutpostRm, api.OutpostRmRequest{Name: name, WithRoutes: true})
		if e, ok := err.(*api.Error); !ok || e.Status != http.StatusForbidden {
			t.Errorf("rm %s: %v", name, err)
		}
		_, err = api.Call[api.OutpostAddResponse](ctx(t), j.API, http.MethodPost, api.PathOutpostRekey, api.OutpostRekeyRequest{Name: name})
		if e, ok := err.(*api.Error); !ok || e.Status != http.StatusForbidden {
			t.Errorf("rekey %s: %v", name, err)
		}
	}
	if len(re.Store.Snapshot().Outposts) != 2 {
		t.Fatal("an Outpost changed the Outposts")
	}
}

func TestRekeyStopsTheOldKeyAtOnceAndTheNewTokenKeepsNameAddressAndRoutes(t *testing.T) {
	svc := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	old := re.JoinOutpost(t, "nas")
	re.AddRoute(t, "a.test", "http://"+svc.Addr(), "nas")
	if r, _ := get(t, re.Caddy, H1, "a.test", "/"); r.StatusCode != 200 {
		t.Fatalf("before rekey: %d", r.StatusCode)
	}
	before := re.Store.Snapshot().Outposts[0]

	resp, err := outpostRekey(t, re, "nas")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Name != "nas" || resp.Address != before.Address.String() || resp.Token == "" {
		t.Fatalf("response %+v", resp)
	}
	for _, k := range edgePeers(t, re) {
		if k == before.PublicKey.Hex() {
			t.Fatal("the old key is still a peer")
		}
	}

	old.Stop()
	re.Connect(t, "nas", resp.Token, mustTempDir(t))
	after := re.Store.Snapshot()
	if len(after.Outposts) != 1 || after.Outposts[0].Address != before.Address || len(after.Routes) != 1 || after.Outposts[0].PublicKey == before.PublicKey {
		t.Fatalf("state after rejoin %+v", after)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		r, _ := get(t, re.Caddy, H1, "a.test", "/")
		if r.StatusCode == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the Route does not serve again: %d", r.StatusCode)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
