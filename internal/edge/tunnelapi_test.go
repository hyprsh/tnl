package edge

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/state"
)

func TestTheTunnelAPICallerIsTheOutpostWithThatTunnelAddress(t *testing.T) {
	st := &state.State{Outposts: []state.Outpost{
		{Name: "nas", Address: netip.MustParseAddr("100.64.0.2")},
		{Name: "laptop", Address: netip.MustParseAddr("100.64.0.3")},
	}}
	for remote, want := range map[string]api.Actor{
		"100.64.0.2:51234": api.OutpostActor("nas"),
		"100.64.0.3:80":    api.OutpostActor("laptop"),
	} {
		got, err := actorBySource(st, remote)
		if err != nil || got != want {
			t.Errorf("%s: %q, %v, want %q", remote, got, err, want)
		}
	}
}

func TestTheTunnelAPIRefusesASourceNoOutpostHas(t *testing.T) {
	st := &state.State{Outposts: []state.Outpost{{Name: "nas", Address: netip.MustParseAddr("100.64.0.2")}}}
	for remote, wantErr := range map[string]string{
		"100.64.0.9:1": "no Outpost has the Tunnel address 100.64.0.9",
		"not an addr":  "no Outpost has the source address",
		"":             "no Outpost has the source address",
	} {
		got, err := actorBySource(st, remote)
		if err == nil || got != "" || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("%q: %q, %v, want an error with %q", remote, got, err, wantErr)
		}
	}
}
