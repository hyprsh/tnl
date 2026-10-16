//go:build outpost

package main

import (
	"fmt"
	"strings"
)

const buildName = "outpost"

// An Outpost-only build has no Edge, links no Caddy, and manages no Outposts,
// Zones or Routes: every Edge command says so and points at the full build.
// A ticket that adds an Edge-only command adds one line here.
var edgeOnlyCommands = []string{
	"edge",
	"outpost",
	"route add",
	"route rm",
	"zone add",
}

func init() {
	refusedGroups := map[string]*group{"route": routeGroup, "zone": zoneGroup}
	for _, name := range edgeOnlyCommands {
		refuse := needsFullBuild(name)
		if grp, sub, ok := strings.Cut(name, " "); ok {
			refusedGroups[grp].subs[sub] = refuse
		} else {
			commands[name] = refuse
		}
	}
}

func needsFullBuild(command string) func([]string) error {
	return func([]string) error {
		return fmt.Errorf("this is the Outpost build; `tnl %s` needs the full build, on the Edge host", command)
	}
}
