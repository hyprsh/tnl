package harness

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/credential"
	"github.com/hyprsh/tnl/internal/edge"
	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/remind"
	"github.com/hyprsh/tnl/internal/state"
)

// declaredEdge is what an operator holds before the Edge first starts: the
// Edge's private key (from `tnl keygen`) and the port its credentials name.
type declaredEdge struct {
	key  keys.Key
	port int
}

func newDeclaredEdge(t *testing.T) declaredEdge {
	t.Helper()
	priv, _, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return declaredEdge{key: priv, port: freePort(t, true)}
}

// outpost does what `tnl credential <name> --address 127.0.0.1` does: the line
// for the Edge and the credential for the Outpost.
func (d declaredEdge) outpost(t *testing.T, name string) (state.DeclaredOutpost, string) {
	t.Helper()
	cred, pub, err := credential.Mint(d.key, name, "127.0.0.1", d.port)
	if err != nil {
		t.Fatal(err)
	}
	return state.DeclaredOutpost{Name: name, Key: pub}, cred.Encode()
}

func (d declaredEdge) options(opts EdgeOptions) EdgeOptions {
	opts.Key, opts.WireGuardPort = &d.key, d.port
	return opts
}

// An Edge started with only its address, key, one Zone line and one Outpost
// line serves a Route declared by an Outpost started with only its credential
// and a route line. Nothing calls the API to add a Zone or an Outpost.
func TestADeclaredEdgeServesARouteDeclaredByAnOutpostThatHoldsOnlyACredential(t *testing.T) {
	svc := NewHTTPService(t, echoHost())
	d := newDeclaredEdge(t)
	line, cred := d.outpost(t, "nas")
	re := StartEdgeWith(t, d.options(EdgeOptions{
		Pebble: StartPebble(t, PebbleOptions{}),
		Zones:  []state.DeclaredZone{{Name: "test"}}, Outposts: []state.DeclaredOutpost{line},
	}))
	st := re.Store.Snapshot()
	if len(st.Zones) != 1 || !st.Zones[0].Declared || len(st.Outposts) != 1 || !st.Outposts[0].Declared {
		t.Fatalf("zones %+v outposts %+v", st.Zones, st.Outposts)
	}
	if got := st.Outposts[0].Address; got != addr.Derive("nas") || st.Outposts[0].OneTimeKey != nil {
		t.Fatalf("outpost %+v", st.Outposts[0])
	}

	j := re.ConnectCredential(t, "nas", cred, ConnectOptions{Declaration: []outpostLine{DeclaredLine("A", "a.test", "http://"+svc.Addr())}})
	waitFor(t, "the line to apply", 10*time.Second, func() bool { return len(re.Store.Snapshot().Routes) == 1 })
	waitServed(t, re, "a.test")
	if _, body := get(t, re.Caddy, H1, "a.test", "/"); body != "a.test" {
		t.Fatalf("answered %q", body)
	}
	if j.Addr != addr.Derive("nas") {
		t.Errorf("the Outpost's Tunnel address is %v", j.Addr)
	}
	// An Outpost with a credential writes no state.json: only its socket is there.
	entries, _ := os.ReadDir(j.StateDir)
	for _, e := range entries {
		if e.Name() == "state.json" {
			t.Error("the Outpost wrote a state.json")
		}
	}
}

func TestAnExistingOutpostStateIsLeftAloneAndIgnoredWithAnInfoLine(t *testing.T) {
	d := newDeclaredEdge(t)
	line, cred := d.outpost(t, "nas")
	re := StartEdgeWith(t, d.options(EdgeOptions{Outposts: []state.DeclaredOutpost{line}}))
	dir := mustTempDir(t)
	os.MkdirAll(dir, 0o700)
	stale := []byte(`{"version": 2, "stale": true}`)
	os.WriteFile(filepath.Join(dir, "state.json"), stale, 0o600)
	olog := new(lockedBuf)
	j, err := re.TryConnectWith(t, "nas", "", dir, ConnectOptions{Credential: cred, LogOut: olog})
	if err != nil {
		t.Fatal(err)
	}
	defer j.Stop()
	got, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	if string(got) != string(stale) {
		t.Errorf("state.json was touched: %s", got)
	}
	if countLines(olog, "level=info", "ignoring the state") != 1 {
		t.Errorf("log:\n%s", olog)
	}
}

func TestAJoinTokenAndACredentialTogetherAreRefused(t *testing.T) {
	d := newDeclaredEdge(t)
	line, cred := d.outpost(t, "nas")
	re := StartEdgeWith(t, d.options(EdgeOptions{Outposts: []state.DeclaredOutpost{line}}))
	_, err := re.TryConnectWith(t, "nas", "tnl1.whatever", mustTempDir(t), ConnectOptions{Credential: cred})
	if err == nil || !strings.Contains(err.Error(), "both set") {
		t.Fatalf("%v", err)
	}
}

// The Outpost's name, key and address in a rekeyed line: the Tunnel address,
// Routes and Declaration stay, the new credential works and the old stops at once.
func TestChangingAnOutpostLinesKeyKeepsItsAddressRoutesAndDeclarationAndRevokesTheOldCredential(t *testing.T) {
	svc := NewHTTPService(t, echoHost())
	d := newDeclaredEdge(t)
	line, cred := d.outpost(t, "nas")
	opts := d.options(EdgeOptions{Zones: []state.DeclaredZone{{Name: "test"}}, Outposts: []state.DeclaredOutpost{line}})
	re := StartEdgeWith(t, opts)
	old := re.ConnectCredential(t, "nas", cred, ConnectOptions{Declaration: []outpostLine{DeclaredLine("A", "a.test", "http://"+svc.Addr())}})
	waitFor(t, "the line to apply", 10*time.Second, func() bool { return len(re.Store.Snapshot().Routes) == 1 })
	before := re.Store.Snapshot().Outposts[0]
	old.Stop()
	re.Stop()

	newLine, newCred := d.outpost(t, "nas")
	opts.StateDir, opts.Outposts = re.StateDir, []state.DeclaredOutpost{newLine}
	elog := new(lockedBuf)
	opts.LogOut, opts.LogLevel = elog, slog.LevelInfo
	again := StartEdgeWith(t, opts)
	after := again.Store.Snapshot()
	o := after.Outposts[0]
	if o.Address != before.Address || o.PublicKey != newLine.Key || o.PublicKey == before.PublicKey {
		t.Fatalf("before %+v after %+v", before, o)
	}
	if len(after.Routes) != 1 || len(after.DeclarationOf("nas")) != 1 {
		t.Fatalf("routes %v declaration %v", after.Routes, after.DeclarationOf("nas"))
	}
	if countLines(elog, "level=info", "outpost rekeyed") != 1 {
		t.Errorf("log:\n%s", elog)
	}

	// The old credential's Tunnel gets no answer; the new one works.
	stale, err := again.TryConnectWith(t, "nas", "", mustTempDir(t), ConnectOptions{Credential: cred, NoHandshake: true})
	if err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if _, err := api.Call[api.Info](cctx, stale.API, http.MethodGet, api.PathInfo, nil); err == nil {
		t.Error("the old credential still reaches the Edge")
	}
	stale.Stop()
	fresh := again.ConnectCredential(t, "nas", newCred, ConnectOptions{})
	if _, err := api.Call[api.Info](ctx(t), fresh.API, http.MethodGet, api.PathInfo, nil); err != nil {
		t.Fatalf("the new credential: %v", err)
	}
}

func TestRestartingWithEveryLineRemovedKeepsEverythingClearsTheMarksAndWarns(t *testing.T) {
	svc := NewHTTPService(t, echoHost())
	d := newDeclaredEdge(t)
	line, cred := d.outpost(t, "nas")
	opts := d.options(EdgeOptions{Zones: []state.DeclaredZone{{Name: "test"}}, Outposts: []state.DeclaredOutpost{line}})
	re := StartEdgeWith(t, opts)
	j := re.ConnectCredential(t, "nas", cred, ConnectOptions{Declaration: []outpostLine{DeclaredLine("A", "a.test", "http://"+svc.Addr())}})
	waitFor(t, "the line to apply", 10*time.Second, func() bool { return len(re.Store.Snapshot().Routes) == 1 })
	j.Stop()
	re.Stop()

	elog := new(lockedBuf)
	again := StartEdgeWith(t, EdgeOptions{StateDir: re.StateDir, WireGuardPort: d.port, Key: &d.key, LogOut: elog, LogLevel: slog.LevelInfo})
	st := again.Store.Snapshot()
	if len(st.Zones) != 1 || len(st.Outposts) != 1 || len(st.Routes) != 1 || st.Zones[0].Declared || st.Outposts[0].Declared {
		t.Fatalf("zones %+v outposts %+v routes %+v", st.Zones, st.Outposts, st.Routes)
	}
	if countLines(elog, "level=warn", "TNL_ZONE_*") != 1 || countLines(elog, "level=warn", "TNL_OUTPOST_*") != 1 {
		t.Errorf("log:\n%s", elog)
	}
	// Afterwards the CLI can change them again.
	again.AddZone(t, "other.test")
	if _, err := api.Call[api.OutpostAddReply](ctx(t), again.API, http.MethodPost, api.PathOutposts, api.OutpostAddRequest{Name: "laptop"}); err != nil {
		t.Fatal(err)
	}
	// The counter skipped nothing it should not, and the state is still valid.
	if got := len(again.Store.Snapshot().Outposts); got != 2 {
		t.Fatalf("%d outposts", got)
	}
}

func TestRemovingAZoneLineWhoseZoneHoldsACLIRouteExitsAndLeavesStateJSONUnchanged(t *testing.T) {
	re := StartEdgeWith(t, EdgeOptions{})
	re.AddZone(t, "example.com")
	re.AddZone(t, "other.org")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.example.com", "http://127.0.0.1:1", "nas")
	re.AddRoute(t, "git.example.com", "http://127.0.0.1:1", "nas")
	re.Stop()
	file := filepath.Join(re.StateDir, "state.json")
	before, _ := os.ReadFile(file)

	_, err := TryStartEdgeWith(t, EdgeOptions{StateDir: re.StateDir, Zones: []state.DeclaredZone{{Name: "other.org"}}})
	var ce *edge.ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("not a config error: %v", err)
	}
	want := "example.com is no longer declared, but app.example.com, git.example.com are in it; remove them or declare the Zone again"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q", err)
	}
	after, _ := os.ReadFile(file)
	if string(after) != string(before) {
		t.Error("state.json changed")
	}
}

func TestRemovingAZoneLineWhoseRoutesAllComeFromDeclarationsPutsThemBackToWaiting(t *testing.T) {
	svc := NewHTTPService(t, echoHost())
	d := newDeclaredEdge(t)
	line, cred := d.outpost(t, "nas")
	opts := d.options(EdgeOptions{Zones: []state.DeclaredZone{{Name: "test"}, {Name: "gone.org"}}, Outposts: []state.DeclaredOutpost{line}})
	re := StartEdgeWith(t, opts)
	j := re.ConnectCredential(t, "nas", cred, ConnectOptions{Declaration: []outpostLine{
		DeclaredLine("A", "a.test", "http://"+svc.Addr()),
		DeclaredLine("B", "b.gone.org", "http://"+svc.Addr()),
	}})
	waitFor(t, "both lines to apply", 10*time.Second, func() bool { return len(re.Store.Snapshot().Routes) == 2 })
	j.Stop()
	re.Stop()

	opts.StateDir, opts.Zones = re.StateDir, []state.DeclaredZone{{Name: "test"}}
	elog := new(lockedBuf)
	opts.LogOut, opts.LogLevel = elog, slog.LevelInfo
	again := StartEdgeWith(t, opts)
	st := again.Store.Snapshot()
	if len(st.Routes) != 1 || len(st.Zones) != 1 || len(st.DeclarationOf("nas")) != 2 || st.WaitingReason("nas", "b.gone.org") != "outside every Zone" {
		t.Fatalf("zones %+v routes %+v waiting %q", st.Zones, st.Routes, st.WaitingReason("nas", "b.gone.org"))
	}
	if countLines(elog, "level=warn", "zone removed", "gone.org") != 1 {
		t.Errorf("log:\n%s", elog)
	}
}

func TestWhileTheEdgeDeclaresItsZonesAndOutpostsTheCLIChangesAreRefused(t *testing.T) {
	d := newDeclaredEdge(t)
	line, _ := d.outpost(t, "nas")
	re := StartEdgeWith(t, d.options(EdgeOptions{Zones: []state.DeclaredZone{{Name: "test"}}, Outposts: []state.DeclaredOutpost{line}}))
	zones := "Zones are declared; change TNL_ZONE_* and restart the Edge"
	outposts := "Outposts are declared; change TNL_OUTPOST_* and restart the Edge"
	calls := []struct {
		name, path, want string
		body             any
	}{
		{"zone add", api.PathZones, zones, api.ZoneAddRequest{Name: "other.org"}},
		{"zone edit", "/v1/zones/edit", zones, api.ZoneEditRequest{Name: "test", PerHostname: true}},
		{"zone rm", "/v1/zones/rm", zones, api.ZoneRmRequest{Name: "test"}},
		{"outpost add", api.PathOutposts, outposts, api.OutpostAddRequest{Name: "laptop"}},
		{"outpost rekey", "/v1/outposts/rekey", outposts, api.OutpostRekeyRequest{Name: "nas"}},
		{"outpost rm", "/v1/outposts/rm", outposts, api.OutpostRmRequest{Name: "nas"}},
	}
	for _, c := range calls {
		_, err := api.Call[map[string]any](ctx(t), re.API, http.MethodPost, c.path, c.body)
		var ae *api.Error
		if !errors.As(err, &ae) || ae.Status != http.StatusConflict || ae.Message != c.want {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	st := re.Store.Snapshot()
	if len(st.Zones) != 1 || len(st.Outposts) != 1 {
		t.Fatalf("something changed: %+v %+v", st.Zones, st.Outposts)
	}
}

func TestNoOutpostJoinsAnEdgeThatDeclaresItsOutposts(t *testing.T) {
	d := newDeclaredEdge(t)
	line, cred := d.outpost(t, "nas")
	re := StartEdgeWith(t, d.options(EdgeOptions{Outposts: []state.DeclaredOutpost{line}}))
	j := re.ConnectCredential(t, "nas", cred, ConnectOptions{})
	_, pub, _ := keys.Generate()
	_, err := api.Call[api.JoinReply](ctx(t), j.API, http.MethodPost, api.PathJoin, api.JoinRequest{PublicKey: pub, Version: harnessVersion})
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Status != http.StatusForbidden || !strings.Contains(ae.Message, "declares its Outposts, so no Outpost joins") {
		t.Fatalf("%v", err)
	}
}

func TestAnEdgeStartedWithAnotherKeyThanStateJSONHoldsRefusesToStart(t *testing.T) {
	d := newDeclaredEdge(t)
	re := StartEdgeWith(t, d.options(EdgeOptions{}))
	re.Stop()
	other, _, _ := keys.Generate()
	elog := new(lockedBuf)
	_, err := TryStartEdgeWith(t, EdgeOptions{StateDir: re.StateDir, Key: &other, KeySource: "TNL_KEY_FILE", LogOut: elog})
	var ce *edge.ConfigError
	if !errors.As(err, &ce) || err.Error() != "state.json holds a different Edge key than TNL_KEY_FILE" {
		t.Fatalf("%v", err)
	}
	for _, k := range []keys.Key{d.key, other} {
		text, _ := k.MarshalText()
		if strings.Contains(elog.String(), string(text)) || strings.Contains(err.Error(), string(text)) {
			t.Error("a key leaked")
		}
	}
	// Without a key the Edge starts as before.
	StartEdgeWith(t, EdgeOptions{StateDir: re.StateDir})
}

func TestAWildcardZoneWhoseTokenCheckFailsAtStartLeavesTheEdgeRunningAndLogsAnError(t *testing.T) {
	StartFakeDNS(t, "example.test", dnsToken)
	const wrong = "wr0ng-T0KEN-value"
	elog := new(lockedBuf)
	re := StartEdgeWith(t, EdgeOptions{
		Pebble: StartPebble(t, PebbleOptions{}), LogOut: elog, LogLevel: slog.LevelDebug,
		Zones: []state.DeclaredZone{{Name: "example.test", Provider: "fake", Token: wrong}},
	})
	waitFor(t, "the error", 10*time.Second, func() bool { return countLines(elog, "level=error", "token check failed", "example.test") == 1 })
	st := re.Store.Snapshot()
	if len(st.Zones) != 1 || st.Zones[0].Token != wrong || !st.Zones[0].Declared {
		t.Fatalf("the Zone was not kept: %+v", st.Zones)
	}
	if strings.Contains(elog.String(), wrong) {
		t.Errorf("the log carries the token:\n%s", elog)
	}
	// The Edge still answers.
	if _, err := api.Call[api.Info](ctx(t), re.API, http.MethodGet, api.PathInfo, nil); err != nil {
		t.Fatal(err)
	}
}

// No log line carries a token, a key or a credential.
func TestNoLogLineCarriesATokenAKeyOrACredential(t *testing.T) {
	StartFakeDNS(t, "example.test", dnsToken)
	d := newDeclaredEdge(t)
	line, cred := d.outpost(t, "nas")
	elog, olog := new(lockedBuf), new(lockedBuf)
	re := StartEdgeWith(t, d.options(EdgeOptions{
		Pebble: StartPebble(t, PebbleOptions{}), LogOut: elog, LogLevel: slog.LevelDebug,
		Zones:    []state.DeclaredZone{{Name: "example.test", Provider: "fake", Token: dnsToken}},
		Outposts: []state.DeclaredOutpost{line},
	}))
	j := re.ConnectCredential(t, "nas", cred, ConnectOptions{LogOut: olog})
	defer j.Stop()
	time.Sleep(300 * time.Millisecond)
	edgeKey, _ := d.key.MarshalText()
	outKey, _ := credentialKey(t, cred).MarshalText()
	for name, log := range map[string]*lockedBuf{"Edge": elog, "Outpost": olog} {
		for _, secret := range []string{dnsToken, string(edgeKey), string(outKey), cred, strings.TrimPrefix(cred, "tnlc1.")} {
			if strings.Contains(log.String(), secret) {
				t.Errorf("the %s's log carries %q", name, secret[:6]+"…")
			}
		}
	}
}

// The failed check is repeated on the reminder schedule, with an error each
// time, and stops when the token is fixed at the provider.
func TestAFailedTokenCheckAtStartIsRepeatedOnTheReminderScheduleUntilItPasses(t *testing.T) {
	fake := StartFakeDNS(t, "example.test", dnsToken)
	fake.SetToken("rotated-T0KEN")
	elog := new(lockedBuf)
	StartEdgeWith(t, EdgeOptions{
		Pebble: StartPebble(t, PebbleOptions{}), LogOut: elog, LogLevel: slog.LevelInfo,
		Reminders: remind.Schedule{Every: 400 * time.Millisecond},
		Zones:     []state.DeclaredZone{{Name: "example.test", Provider: "fake", Token: dnsToken}},
	})
	waitFor(t, "a reminder error", 10*time.Second, func() bool { return countLines(elog, "level=error", "token check failed", "failing_for") >= 1 })
	fake.SetToken(dnsToken)
	waitFor(t, "the check to pass", 10*time.Second, func() bool { return countLines(elog, "level=info", "token check passes now") == 1 })
	if strings.Contains(elog.String(), dnsToken) || strings.Contains(elog.String(), "rotated-T0KEN") {
		t.Errorf("the log carries a token:\n%s", elog)
	}
}

func credentialKey(t *testing.T, cred string) keys.Key {
	t.Helper()
	c, err := credential.Decode(cred)
	if err != nil {
		t.Fatal(err)
	}
	return c.PrivateKey
}
