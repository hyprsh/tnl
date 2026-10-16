//go:build !outpost

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/declare"
	"github.com/hyprsh/tnl/internal/state"
)

func TestStatusOfAHealthyEdgeExitsZero(t *testing.T) {
	runningEdge(t)
	tnl("zone", "add", "example.com")
	out, code, err := tnl("status")
	if err != nil || code != 0 {
		t.Fatalf("%q %v %d", out, err, code)
	}
	for _, want := range []string{"Zones:", "tnl.example.com, WireGuard port 51820", "Nothing needs attention"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
}

func TestStatusExitsOneAndStaysQuietWhenSomethingNeedsAttention(t *testing.T) {
	store := runningEdge(t)
	tnl("outpost", "add", "newbox")
	err := store.Update(api.ActorLocal, func(st *state.State) error {
		st.Declarations = []state.Declaration{{Outpost: "newbox", Lines: []state.DeclaredLine{
			{Line: declare.Line{Hostname: "x.other.org", Service: "http://new:1"}, Declared: time.Now()},
		}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	out, errOut, code, err := tnlBoth("status")
	if code != 1 || !silent(err) || errOut != "" {
		t.Fatalf("code %d, err %v, stderr %q", code, err, errOut)
	}
	if !strings.Contains(out, "Needs attention\n  - newbox declares x.other.org, which is outside every Zone\n") {
		t.Errorf("output:\n%s", out)
	}
}

func TestStatusJSONIsTheReplyAsItCame(t *testing.T) {
	runningEdge(t)
	out, code, err := tnl("status", "--json")
	if err != nil || code != 0 {
		t.Fatalf("%q %v %d", out, err, code)
	}
	var s api.EdgeStatus
	if err := json.Unmarshal([]byte(out), &s); err != nil || s.Role != api.RoleEdge || s.Attention == nil {
		t.Fatalf("%v %+v\n%s", err, s, out)
	}
	if !strings.Contains(out, `"attention": []`) {
		t.Errorf("an empty list must print as [], not null:\n%s", out)
	}
}
