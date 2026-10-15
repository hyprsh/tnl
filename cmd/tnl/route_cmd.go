package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"text/tabwriter"

	"github.com/hyprsh/tnl/internal/api"
)

var routeGroup = newGroup("route")

func init() {
	routeGroup.subs["ls"] = routeLs
}

// routeLs prints HOSTNAME, OUTPOST, SERVICE and OPTIONS. STATUS comes in slice 10.
func routeLs(args []string) error {
	c := newClientCommand("route ls")
	pos, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return fmt.Errorf("route ls takes no arguments, got %q", pos[0])
	}
	list, err := api.Call[api.RouteList](context.Background(), c.client(), http.MethodGet, api.PathRoutes, nil)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "HOSTNAME\tOUTPOST\tSERVICE\tOPTIONS")
	for _, r := range list.Routes {
		opts := strings.Join(r.Options, " ")
		if opts == "" {
			opts = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Hostname, r.Outpost, r.Service, opts)
	}
	return tw.Flush()
}
