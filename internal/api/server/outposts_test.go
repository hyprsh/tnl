package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/token"
)

func addOutpost(c *api.Client, name string) (api.OutpostAddResponse, error) {
	return api.Call[api.OutpostAddResponse](context.Background(), c, http.MethodPost, api.PathOutposts, api.OutpostAddRequest{Name: name})
}

func TestOutpostAddReturnsATokenForTheNextAddress(t *testing.T) {
	f := start(t, "1.2.3", api.ActorLocal)
	for i, want := range []string{"100.64.0.2", "100.64.0.3"} {
		name := []string{"laptop", "nas-1"}[i]
		resp, err := addOutpost(f.client, name)
		if err != nil {
			t.Fatal(err)
		}
		tok, err := token.Decode(resp.Token)
		if err != nil {
			t.Fatal(err)
		}
		st := f.store.Snapshot()
		if tok.Address != "tnl.example.com" || tok.Port != 51820 || tok.EdgeVersion != "1.2.3" {
			t.Errorf("token %+v", tok)
		}
		if tok.EdgeKey != st.PrivateKey.Public() {
			t.Error("token does not carry the Edge's public key")
		}
		if tok.TunnelAddr != netip.MustParseAddr(want) || resp.Address != want {
			t.Errorf("address %v / %v, want %s", tok.TunnelAddr, resp.Address, want)
		}
		o := st.Outposts[i]
		if o.Name != name || o.Address != tok.TunnelAddr || o.PublicKey != tok.OneTimeKey.Public() {
			t.Errorf("state %+v does not match token %+v", o, tok)
		}
	}
	if f.store.Snapshot().NextAddr != 2 {
		t.Errorf("counter %d", f.store.Snapshot().NextAddr)
	}
}

func TestOutpostAddRefusesDuplicatesAndBadNames(t *testing.T) {
	f := start(t, "1.2.3", api.ActorLocal)
	if _, err := addOutpost(f.client, "laptop"); err != nil {
		t.Fatal(err)
	}
	cases := map[string]int{
		"laptop": http.StatusConflict, "": http.StatusBadRequest, "My_Box": http.StatusBadRequest,
		"a.b": http.StatusBadRequest, "-a": http.StatusBadRequest, "a-": http.StatusBadRequest,
		"has space": http.StatusBadRequest, strings.Repeat("a", 64): http.StatusBadRequest,
	}
	for name, status := range cases {
		_, err := addOutpost(f.client, name)
		var ae *api.Error
		if !errors.As(err, &ae) || ae.Status != status {
			t.Errorf("%q: err = %v, want status %d", name, err, status)
		}
	}
	if n := len(f.store.Snapshot().Outposts); n != 1 {
		t.Errorf("%d Outposts, want 1", n)
	}
	if f.store.Snapshot().NextAddr != 1 {
		t.Error("a refused add used up an address")
	}
}

func TestOutpostAddIsRefusedForAnOutpost(t *testing.T) {
	f := start(t, "1.2.3", api.OutpostActor("laptop"))
	_, err := addOutpost(f.client, "other")
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Status != http.StatusForbidden {
		t.Fatalf("err = %v", err)
	}
}
