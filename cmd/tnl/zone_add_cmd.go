//go:build !outpost

package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hyprsh/tnl/internal/api"
)

func init() {
	zoneGroup.subs["add"] = zoneAdd
}

// zoneAdd saves a per-Hostname Zone. Wildcard Zones come in slice 5.
func zoneAdd(args []string) error {
	c := newClientCommand("zone add")
	pos, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: tnl zone add <zone>")
	}
	z, err := api.Call[api.ZoneInfo](context.Background(), c.client(), http.MethodPost, api.PathZones, api.ZoneAddRequest{Name: pos[0]})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "added Zone %s (%s)\n", z.Name, z.Mode)
	return nil
}
