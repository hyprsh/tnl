package main

import (
	"strings"

	"github.com/hyprsh/tnl/internal/api"
)

var routeGroup = newGroup("route")

func init() {
	// STATUS comes in slice 10.
	routeGroup.subs["ls"] = lsCommand("route ls", api.PathRoutes, "HOSTNAME\tOUTPOST\tSERVICE\tOPTIONS", func(l api.RouteList) (rows [][]string) {
		for _, r := range l.Routes {
			rows = append(rows, []string{r.Hostname, r.Outpost, r.Service, orDash(strings.Join(r.Options, " "))})
		}
		return rows
	})
}
