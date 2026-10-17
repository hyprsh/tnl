//go:build !outpost

package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/hyprsh/tnl/internal/api"
)

func init() {
	zoneGroup.subs["rm"] = zoneRm
}

// zoneRm removes a Zone. Routes a parent Zone contains move to it; Routes left
// outside every Zone refuse the removal, or go with --with-routes.
func zoneRm(args []string) error {
	c := newClientCommand("zone rm")
	with := c.fs.Bool("with-routes", false, "also remove the Routes that no other Zone contains")
	pos, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErr("zone rm", "usage: tnl zone rm <zone> [--with-routes]")
	}
	r, err := api.Call[api.ZoneRmReply](context.Background(), c.client(), http.MethodPost, api.PathZoneRm,
		api.ZoneRmRequest{Name: pos[0], WithRoutes: *with})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "removed Zone %s\n", r.Name)
	if len(r.Moved) > 0 {
		fmt.Fprintf(stdout, "moved to a parent Zone: %s\n", strings.Join(r.Moved, ", "))
	}
	if len(r.Removed) > 0 {
		fmt.Fprintf(stdout, "removed its Routes: %s\n", strings.Join(r.Removed, ", "))
	}
	if len(r.Waiting) > 0 {
		fmt.Fprintf(stdout, "declared Routes waiting again: %s\n", strings.Join(r.Waiting, ", "))
	}
	printHandovers(r.Handovers)
	printWarnings(r.Warnings)
	return nil
}
