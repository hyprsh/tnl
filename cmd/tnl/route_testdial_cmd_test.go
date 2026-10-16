//go:build !outpost

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/edge/routes"
)

type refusingDialer struct{ dialed []string }

func (d *refusingDialer) TestDial(_ context.Context, r routes.Route) error {
	d.dialed = append(d.dialed, r.Service)
	return &routes.DialFailure{Kind: routes.DialNoAnswer, Detail: "refused"}
}

func TestRouteAddAndEditPrintTheTestDialsWarningsAndExitZero(t *testing.T) {
	d := &refusingDialer{}
	edgeTestDialer = d
	t.Cleanup(func() { edgeTestDialer = nil })
	runningEdge(t)
	tnl("zone", "add", "example.com")
	tnl("outpost", "add", "nas")

	_, errOut, code, err := tnlBoth("route", "add", "a.example.com", "http://a:80", "--outpost", "nas")
	if err != nil || code != 0 || !strings.Contains(errOut, "warning: a:80 doesn't answer") {
		t.Fatalf("add: err=%v code=%d stderr=%q", err, code, errOut)
	}
	_, errOut, code, err = tnlBoth("route", "edit", "a.example.com", "--host-header", "x.lan")
	if err != nil || code != 0 || errOut != "" || len(d.dialed) != 1 {
		t.Fatalf("option-only edit: err=%v code=%d stderr=%q dialed=%v", err, code, errOut, d.dialed)
	}
	_, errOut, code, err = tnlBoth("route", "edit", "a.example.com", "--service", "http://b:80")
	if err != nil || code != 0 || !strings.Contains(errOut, "warning: b:80 doesn't answer") || len(d.dialed) != 2 {
		t.Fatalf("service edit: err=%v code=%d stderr=%q dialed=%v", err, code, errOut, d.dialed)
	}
}
