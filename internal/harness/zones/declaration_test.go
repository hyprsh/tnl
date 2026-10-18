package zones

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/declare"

	. "github.com/hyprsh/tnl/internal/harness"
)

// reconnect starts the Outpost again from its state directory.
func reconnect(t *testing.T, re *RealEdge, name, stateDir string, opts ConnectOptions) *JoinedOutpost {
	t.Helper()
	opts.JoinTimeout = 3 * time.Second
	j, err := re.TryConnectWith(t, name, "", stateDir, opts)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func routeHosts(re *RealEdge) []string {
	var hosts []string
	for _, r := range re.Store.Snapshot().Routes {
		hosts = append(hosts, r.Hostname)
	}
	return hosts
}

func countLines(log *LockedBuf, parts ...string) int {
	n := 0
	for _, l := range strings.Split(log.String(), "\n") {
		ok := l != ""
		for _, p := range parts {
			ok = ok && strings.Contains(l, p)
		}
		if ok {
			n++
		}
	}
	return n
}

func sendDeclaration(t *testing.T, j *JoinedOutpost, lines ...declare.Line) api.DeclarationReply {
	t.Helper()
	r, err := api.Call[api.DeclarationReply](Ctx(t), j.API, http.MethodPost, api.PathDeclaration, api.DeclarationRequest{Routes: lines})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAnOutpostWithTwoRouteLinesServesBothWithNoRouteCall(t *testing.T) {
	svc := NewHTTPService(t, EchoHost())
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	tok := re.AddOutpost(t, "nas")
	re.ConnectWith(t, "nas", tok, ConnectOptions{Declaration: []OutpostLine{
		DeclaredLine("A", "a.test", "http://"+svc.Addr()),
		DeclaredLine("B", "b.test", "http://"+svc.Addr()),
	}})
	WaitFor(t, "both lines to apply", 10*time.Second, func() bool { return len(re.Store.Snapshot().Routes) == 2 })
	for _, host := range []string{"a.test", "b.test"} {
		WaitServed(t, re, host)
		if _, body := Get(t, re.Caddy, H1, host, "/"); body != host {
			t.Errorf("%s answered %q", host, body)
		}
	}
}

func TestRestartingWithOneLineChangedAndOneRemovedEditsAndRemovesExactlyThose(t *testing.T) {
	svc := NewHTTPService(t, EchoHost())
	other := NewHTTPService(t, EchoHost())
	elog := new(LockedBuf)
	re := StartEdgeWith(t, EdgeOptions{LogOut: elog, LogLevel: slog.LevelInfo})
	re.AddZone(t, "test")
	tok := re.AddOutpost(t, "nas")
	j := re.ConnectWith(t, "nas", tok, ConnectOptions{Declaration: []OutpostLine{
		DeclaredLine("KEEP", "keep.test", "http://"+svc.Addr()),
		DeclaredLine("EDIT", "edit.test", "http://"+svc.Addr()),
		DeclaredLine("GONE", "gone.test", "http://"+svc.Addr()),
	}})
	WaitFor(t, "three Routes", 10*time.Second, func() bool { return len(re.Store.Snapshot().Routes) == 3 })
	first := re.Store.Snapshot().DeclarationOf("nas")
	j.Stop()

	olog := new(LockedBuf)
	reconnect(t, re, "nas", j.StateDir, ConnectOptions{LogOut: olog, Declaration: []OutpostLine{
		DeclaredLine("KEEP", "keep.test", "http://"+svc.Addr()),
		DeclaredLine("EDIT", "edit.test", "http://"+other.Addr()),
	}})
	WaitFor(t, "the diff", 10*time.Second, func() bool {
		r, ok := re.Store.Snapshot().Route("edit.test")
		return ok && r.Service == "http://"+other.Addr()
	})
	st := re.Store.Snapshot()
	if hosts := routeHosts(re); len(hosts) != 2 || st.WaitingReason("nas", "gone.test") == "" {
		t.Fatalf("Routes %v", hosts)
	}
	if k, _ := st.Route("keep.test"); k.Service != "http://"+svc.Addr() {
		t.Fatalf("keep.test changed: %+v", k)
	}
	// The line that stays keeps the time it was first declared.
	if !st.DeclarationOf("nas")[1].Declared.Equal(first[1].Declared) {
		t.Fatalf("keep.test was declared again: %v vs %v", st.DeclarationOf("nas"), first)
	}
	WaitFor(t, "the Outpost's summary", 5*time.Second, func() bool {
		return countLines(olog, "level=info", `msg="Declaration sent"`, "declared=2", "changed=1", "removed=1") == 1
	})
	if countLines(elog, "declared line changed", "edit.test") != 1 || countLines(elog, "declared line removed", "gone.test") != 1 {
		t.Fatalf("Edge log:\n%s", elog.String())
	}
}

func TestAnUnchangedLineKeepsItsOpenStreamWhenTheDeclarationChangesAroundIt(t *testing.T) {
	svc := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sse" {
			SSEHandler(w, r)
			return
		}
		EchoHost().ServeHTTP(w, r)
	}))
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	tok := re.AddOutpost(t, "nas")
	keep, edit := DeclaredLine("KEEP", "keep.test", "http://"+svc.Addr()), DeclaredLine("EDIT", "edit.test", "http://"+svc.Addr())
	j := re.ConnectWith(t, "nas", tok, ConnectOptions{Declaration: []OutpostLine{keep, edit}})
	WaitFor(t, "two Routes", 10*time.Second, func() bool { return len(re.Store.Snapshot().Routes) == 2 })
	WaitServed(t, re, "keep.test")
	WaitServed(t, re, "edit.test")
	nextKeep := OpenStream(t, re, "keep.test")
	nextEdit := OpenStream(t, re, "edit.test")
	if !nextKeep() || !nextEdit() {
		t.Fatal("a stream did not start")
	}
	edited := edit.Line
	edited.HostHeader = "other.lan"
	reply := sendDeclaration(t, j, keep.Line, edited)
	if len(reply.Lines) != 2 || reply.Lines[0].Outcome != api.LineChanged || reply.Lines[1].Outcome != api.LineUnchanged {
		t.Fatalf("reply %+v", reply)
	}
	if !nextKeep() {
		t.Fatal("the unchanged line's stream was cut")
	}
	WaitFor(t, "the changed line's stream to end", 5*time.Second, func() bool { return !nextEdit() })
}

func TestAnUnchangedDeclarationIsNotSentAgain(t *testing.T) {
	svc := NewHTTPService(t, EchoHost())
	elog := new(LockedBuf)
	re := StartEdgeWith(t, EdgeOptions{LogOut: elog, LogLevel: slog.LevelInfo})
	re.AddZone(t, "test")
	tok := re.AddOutpost(t, "nas")
	lines := []OutpostLine{DeclaredLine("A", "a.test", "http://"+svc.Addr())}
	j := re.ConnectWith(t, "nas", tok, ConnectOptions{Declaration: lines})
	WaitFor(t, "the first send", 10*time.Second, func() bool { return countLines(elog, "declaration applied") == 1 })
	j.Stop()
	reconnect(t, re, "nas", j.StateDir, ConnectOptions{Declaration: lines})
	// Many pings later (every 100 ms), the Edge still saw one send.
	// (Waits to show that nothing more is sent, so no condition ends it.)
	time.Sleep(1500 * time.Millisecond)
	if n := countLines(elog, "declaration applied"); n != 1 {
		t.Fatalf("the Declaration was sent %d times", n)
	}
}

func TestWhileAnOutpostDeclaresRouteAddEditAndRmAreRefusedFromBothSides(t *testing.T) {
	svc := NewHTTPService(t, EchoHost())
	re := StartEdgeWith(t, EdgeOptions{})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "pi")
	tok := re.AddOutpost(t, "nas")
	j := re.ConnectWith(t, "nas", tok, ConnectOptions{Declaration: []OutpostLine{DeclaredLine("A", "a.test", "http://"+svc.Addr())}})
	WaitFor(t, "the Route", 10*time.Second, func() bool { return len(re.Store.Snapshot().Routes) == 1 })
	re.AddRoute(t, "pi.test", "http://"+svc.Addr(), "pi")

	const msg = "nas declares its Routes; change `TNL_ROUTE_*` on nas and restart it"
	svcURL := "http://" + svc.Addr()
	other := "http://localhost:9"
	nas, pi := "nas", "pi"
	for name, c := range map[string]*api.Client{"Edge host": re.API, "Outpost": j.CLI} {
		calls := map[string]func() error{
			"add": func() error {
				_, err := api.Call[api.RouteAddReply](Ctx(t), c, http.MethodPost, api.PathRoutes, api.RouteAddRequest{Hostname: "new.test", Service: svcURL, Outpost: nas})
				return err
			},
			"edit": func() error {
				_, err := api.Call[api.RouteEditReply](Ctx(t), c, http.MethodPost, api.PathRouteEdit, api.RouteEditRequest{Hostname: "a.test", Service: &other})
				return err
			},
			"rm": func() error {
				_, err := api.Call[api.RouteRmReply](Ctx(t), c, http.MethodPost, api.PathRouteRm, api.RouteRmRequest{Hostname: "a.test"})
				return err
			},
		}
		if name == "Edge host" {
			calls["move onto"] = func() error {
				_, err := api.Call[api.RouteEditReply](Ctx(t), c, http.MethodPost, api.PathRouteEdit, api.RouteEditRequest{Hostname: "pi.test", Outpost: &nas})
				return err
			}
			calls["move off"] = func() error {
				_, err := api.Call[api.RouteEditReply](Ctx(t), c, http.MethodPost, api.PathRouteEdit, api.RouteEditRequest{Hostname: "a.test", Outpost: &pi})
				return err
			}
		}
		for verb, call := range calls {
			err := call()
			if err == nil || !strings.Contains(err.Error(), msg) {
				t.Errorf("%s %s: %v, want %q", name, verb, err, msg)
			}
		}
	}
	if len(re.Store.Snapshot().Routes) != 2 {
		t.Fatalf("Routes changed: %v", routeHosts(re))
	}
}

func TestAnOutpostThatHadCLIRoutesLosesThemWhenItStartsDeclaringAndGetsThemBackWhenItStops(t *testing.T) {
	svc := NewHTTPService(t, EchoHost())
	elog := new(LockedBuf)
	re := StartEdgeWith(t, EdgeOptions{LogOut: elog, LogLevel: slog.LevelInfo})
	re.AddZone(t, "test")
	j := re.JoinOutpost(t, "nas")
	re.AddRoute(t, "cli.test", "http://"+svc.Addr(), "nas")
	j.Stop()

	olog := new(LockedBuf)
	d := reconnect(t, re, "nas", j.StateDir, ConnectOptions{LogOut: olog, Declaration: []OutpostLine{DeclaredLine("A", "a.test", "http://"+svc.Addr())}})
	WaitFor(t, "the switch", 10*time.Second, func() bool {
		_, cli := re.Store.Snapshot().Route("cli.test")
		_, a := re.Store.Snapshot().Route("a.test")
		return !cli && a
	})
	WaitFor(t, "warn lines on both sides", 5*time.Second, func() bool {
		return countLines(elog, "level=warn", "cli.test", "was removed") == 1 && countLines(olog, "level=warn", "cli.test", "was removed") == 1
	})
	d.Stop()

	// An empty Declaration hands the Outpost back to the CLI.
	reconnect(t, re, "nas", j.StateDir, ConnectOptions{})
	WaitFor(t, "the hand-back", 10*time.Second, func() bool { return len(re.Store.Snapshot().Routes) == 0 })
	if re.Store.Snapshot().Declares("nas") {
		t.Fatal("nas still declares")
	}
	re.AddRoute(t, "cli.test", "http://"+svc.Addr(), "nas")
}

func TestARefusedDeclarationChangesNothing(t *testing.T) {
	svc := NewHTTPService(t, EchoHost())
	re := StartEdgeWith(t, EdgeOptions{})
	re.AddZone(t, "test")
	j := re.JoinOutpost(t, "nas")
	_, err := api.Call[api.DeclarationReply](Ctx(t), j.API, http.MethodPost, api.PathDeclaration, api.DeclarationRequest{Routes: []declare.Line{
		{Hostname: "a.test", Service: "http://" + svc.Addr()},
		{Hostname: "b.test", Service: "ftp://x"},
	}})
	if err == nil {
		t.Fatal("a bad line was accepted")
	}
	if len(re.Store.Snapshot().Routes) != 0 || re.Store.Snapshot().Declares("nas") {
		t.Fatal("a refused Declaration changed the state")
	}
}

func TestALineWhoseServiceRefusesConnectionsIsAppliedAndItsTestDialWarnsInTheOutpostsLog(t *testing.T) {
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{})})
	re.AddZone(t, "test")
	tok := re.AddOutpost(t, "nas")
	addr := ClosedAddr(t)
	olog := new(LockedBuf)
	re.ConnectWith(t, "nas", tok, ConnectOptions{LogOut: olog, Declaration: []OutpostLine{DeclaredLine("A", "a.test", "http://"+addr)}})
	WaitFor(t, "the Route", 10*time.Second, func() bool { return len(re.Store.Snapshot().Routes) == 1 })
	WaitFor(t, "the warn line", 10*time.Second, func() bool {
		return countLines(olog, "level=warn", addr, "doesn't answer") == 1
	})
}

func TestOutpostRmDeletesTheDeclarationAndRekeyKeepsIt(t *testing.T) {
	svc := NewHTTPService(t, EchoHost())
	re := StartEdgeWith(t, EdgeOptions{})
	re.AddZone(t, "test")
	tok := re.AddOutpost(t, "nas")
	re.ConnectWith(t, "nas", tok, ConnectOptions{Declaration: []OutpostLine{DeclaredLine("A", "a.test", "http://"+svc.Addr())}})
	WaitFor(t, "the Route", 10*time.Second, func() bool { return len(re.Store.Snapshot().Routes) == 1 })

	if _, err := api.Call[api.OutpostAddReply](Ctx(t), re.API, http.MethodPost, api.PathOutpostRekey, api.OutpostRekeyRequest{Name: "nas"}); err != nil {
		t.Fatal(err)
	}
	if st := re.Store.Snapshot(); len(st.Routes) != 1 || len(st.DeclarationOf("nas")) != 1 {
		t.Fatalf("rekey lost the Declaration: %+v", st)
	}
	if _, err := api.Call[api.OutpostRmReply](Ctx(t), re.API, http.MethodPost, api.PathOutpostRm, api.OutpostRmRequest{Name: "nas", WithRoutes: true}); err != nil {
		t.Fatal(err)
	}
	if st := re.Store.Snapshot(); len(st.Declarations) != 0 || len(st.Routes) != 0 {
		t.Fatalf("outpost rm left %+v", st.Declarations)
	}
}

func TestALineOutsideEveryZoneWaitsAndIsReportedToTheOutpost(t *testing.T) {
	svc := NewHTTPService(t, EchoHost())
	re := StartEdgeWith(t, EdgeOptions{})
	re.AddZone(t, "test")
	j := re.JoinOutpost(t, "nas")
	reply := sendDeclaration(t, j,
		declare.Line{Hostname: "a.test", Service: "http://" + svc.Addr()},
		declare.Line{Hostname: "b.other", Service: "http://" + svc.Addr()})
	if reply.Lines[1].Outcome != api.LineWaiting || reply.Lines[1].Reason != "outside every Zone" {
		t.Fatalf("reply %+v", reply)
	}
	mine, err := api.Call[api.MyRoutes](Ctx(t), j.API, http.MethodGet, api.PathDeclaration, nil)
	if err != nil || len(mine.Routes) != 2 || mine.Routes[0].Waiting || !mine.Routes[1].Waiting {
		t.Fatalf("%+v, %v", mine, err)
	}
	// Only an Outpost has a Declaration to send or Routes of its own to fetch.
	if _, err := api.Call[api.MyRoutes](Ctx(t), re.API, http.MethodGet, api.PathDeclaration, nil); err == nil {
		t.Fatal("local fetched an Outpost's Routes")
	}
	if _, err := api.Call[api.DeclarationReply](Ctx(t), re.API, http.MethodPost, api.PathDeclaration, api.DeclarationRequest{}); err == nil {
		t.Fatal("local sent a Declaration")
	}
}
