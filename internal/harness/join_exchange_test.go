package harness

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/token"
	"github.com/hyprsh/tnl/internal/wire"
)

// outpostStateFile reads an Outpost's state.json as a generic map.
func outpostStateFile(t *testing.T, dir string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func reachService(t *testing.T, re *RealEdge, j *JoinedOutpost) {
	t.Helper()
	svc := NewEchoService(t)
	conn, err := re.Dial(Ctx(t), j.Outpost, wire.Header{Service: svc.Addr(), Hostname: "app.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("hi"))
	got := make([]byte, 2)
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != "hi" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestJoinSwapsInTheOutpostsOwnKeyAndForgetsTheOneTimeKey(t *testing.T) {
	re := StartEdge(t)
	tokText := re.AddOutpost(t, "laptop")
	tok, err := token.Decode(tokText)
	if err != nil {
		t.Fatal(err)
	}
	if o := re.Store.Snapshot().Outposts[0]; o.OneTimeKey == nil || o.TokenExpires.IsZero() {
		t.Fatalf("an Outpost that hasn't joined holds the one-time public key and the expiry: %+v", o)
	}

	dir := mustTempDir(t)
	j := re.Connect(t, "laptop", tokText, dir)

	state := outpostStateFile(t, dir)
	ownPriv := state["private_key"].(string)
	oneTimePriv, _ := tok.OneTimeKey.MarshalText()
	if ownPriv == string(oneTimePriv) {
		t.Error("the Outpost kept the token's key")
	}
	var priv keys.Key
	if err := priv.UnmarshalText([]byte(ownPriv)); err != nil {
		t.Fatal(err)
	}
	if state["name"] != "laptop" {
		t.Errorf("state.json name = %v", state["name"])
	}
	for k := range state {
		if strings.Contains(k, "one_time") {
			t.Errorf("state.json holds %s", k)
		}
	}

	o := re.Store.Snapshot().Outposts[0]
	if o.PublicKey != priv.Public() {
		t.Error("the Edge's peer is not the Outpost's own public key")
	}
	if o.OneTimeKey != nil || !o.TokenExpires.IsZero() {
		t.Errorf("the Edge kept the one-time key: %+v", o)
	}
	peers := EdgePeers(t, re)
	if len(peers) != 1 || peers[0] != priv.Public().Hex() {
		t.Errorf("WireGuard peers %v, want only %s", peers, priv.Public().Hex())
	}
	reachService(t, re, j)
}

func TestASecondConnectWithTheSameTokenFailsAndTheFirstKeepsWorking(t *testing.T) {
	re := StartEdge(t)
	tok := re.AddOutpost(t, "laptop")
	first := re.Connect(t, "laptop", tok, mustTempDir(t))

	dir := mustTempDir(t)
	if _, err := re.TryConnectWith(t, "laptop", tok, dir, ConnectOptions{JoinTimeout: 2 * time.Second}); err == nil {
		t.Fatal("the used token joined a second Outpost")
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); !os.IsNotExist(err) {
		t.Errorf("state written for the failed join: %v", err)
	}
	reachService(t, re, first)
}

func TestATokenPastItsTTLIsRefusedAndTheStateDirectoryIsUntouched(t *testing.T) {
	re := StartEdge(t)
	tok := re.AddOutpostTTL(t, "laptop", "50ms")
	time.Sleep(150 * time.Millisecond)
	dir := mustTempDir(t)
	_, err := re.TryConnect(t, "laptop", tok, dir)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the state directory was touched: %v", err)
	}
}

func TestRestartWithTheSameTokenStillSetReconnectsFromTheState(t *testing.T) {
	re := StartEdge(t)
	tok := re.AddOutpost(t, "laptop")
	dir := mustTempDir(t)
	j := re.Connect(t, "laptop", tok, dir)
	before := outpostStateFile(t, dir)
	j.Stop()

	logs := &LockedBuf{}
	j2, err := re.TryConnectWith(t, "laptop", tok, dir, ConnectOptions{LogOut: logs, JoinTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, outpostStateFile(t, dir)) {
		t.Error("the state changed")
	}
	if !strings.Contains(logs.String(), "joined=false") {
		t.Errorf("the restart joined again:\n%s", logs)
	}
	reachService(t, re, j2)
}

func TestATokenForAnotherOutpostJoinsAsThatOutpostAndReplacesTheState(t *testing.T) {
	re := StartEdge(t)
	tok := re.AddOutpost(t, "laptop")
	dir := mustTempDir(t)
	re.Connect(t, "laptop", tok, dir).Stop()
	tok2 := re.AddOutpost(t, "nas")

	logs := &LockedBuf{}
	j, err := re.TryConnectWith(t, "nas", tok2, dir, ConnectOptions{LogOut: logs})
	if err != nil {
		t.Fatal(err)
	}
	state := outpostStateFile(t, dir)
	if state["name"] != "nas" || state["address"] != "100.64.0.3" || state["token_sha256"] != token.Hash(tok2) {
		t.Errorf("state.json = %v", state)
	}
	if !strings.Contains(logs.String(), "name=nas") {
		t.Errorf("the start line lacks the name:\n%s", logs)
	}
	reachService(t, re, j)
}

func TestAJoinedOutpostCallingTheJoinPathAgainGets403(t *testing.T) {
	re := StartEdge(t)
	j := re.JoinOutpost(t, "laptop")
	_, pub, _ := keys.Generate()
	req := api.JoinRequest{PublicKey: pub, Version: harnessVersion}
	var ae *api.Error
	_, err := api.Call[api.JoinReply](context.Background(), j.API, http.MethodPost, api.PathJoin, req)
	if !errors.As(err, &ae) || ae.Status != http.StatusForbidden {
		t.Fatalf("err = %v", err)
	}
	_, err = api.Call[api.JoinReply](context.Background(), re.API, http.MethodPost, api.PathJoin, req)
	if !errors.As(err, &ae) || ae.Status != http.StatusForbidden {
		t.Fatalf("local: err = %v", err)
	}
}

func TestTheStartLogLineCarriesTheOutpostsName(t *testing.T) {
	re := StartEdge(t)
	tok := re.AddOutpost(t, "laptop")
	logs := &LockedBuf{}
	if _, err := re.TryConnectWith(t, "laptop", tok, mustTempDir(t), ConnectOptions{LogOut: logs}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "name=laptop") {
		t.Errorf("log:\n%s", logs)
	}
}
