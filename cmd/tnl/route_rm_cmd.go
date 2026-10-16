package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hyprsh/tnl/internal/api"
)

func init() {
	routeGroup.subs["rm"] = routeRm
}

// routeRm removes a Route and closes its connections. An Outpost's call goes
// through its relay and removes only its own Routes.
func routeRm(args []string) error {
	c := newClientCommand("route rm")
	pos, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: tnl route rm <hostname>")
	}
	r, err := api.Call[api.RouteInfo](context.Background(), c.client(), http.MethodPost, api.PathRouteRm,
		api.RouteRmRequest{Hostname: pos[0]})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "removed Route %s (was %s through Outpost %s)\n", r.Hostname, r.Service, r.Outpost)
	return nil
}
