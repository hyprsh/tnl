package main

import (
	"context"
	"fmt"
	"net/http"
	"text/tabwriter"

	"github.com/hyprsh/tnl/internal/api"
)

var zoneGroup = newGroup("zone")

func init() {
	zoneGroup.subs["ls"] = zoneLs
}

// zoneLs prints ZONE, MODE and PROVIDER. CERTIFICATE comes in slice 10.
func zoneLs(args []string) error {
	c := newClientCommand("zone ls")
	pos, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return fmt.Errorf("zone ls takes no arguments, got %q", pos[0])
	}
	list, err := api.Call[api.ZoneList](context.Background(), c.client(), http.MethodGet, api.PathZones, nil)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ZONE\tMODE\tPROVIDER")
	for _, z := range list.Zones {
		provider := z.Provider
		if provider == "" {
			provider = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", z.Name, z.Mode, provider)
	}
	return tw.Flush()
}
