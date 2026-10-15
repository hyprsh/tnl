//go:build outpost

package main

import (
	"errors"
	"fmt"
)

// An Outpost-only build has no Edge, links no Caddy, and manages no Outposts,
// Zones or Routes: those commands say so and point at the full build.
func init() {
	commands["edge"] = func([]string) error { return errors.New("this build is Outpost-only; it has no Edge") }
	commands["outpost"] = needsFullBuild("outpost")
	routeGroup.subs["add"] = needsFullBuild("route add")
	zoneGroup.subs["add"] = needsFullBuild("zone add")
}

func needsFullBuild(command string) func([]string) error {
	return func([]string) error {
		return fmt.Errorf("this is the Outpost build; `tnl %s` needs the full build, on the Edge host", command)
	}
}
