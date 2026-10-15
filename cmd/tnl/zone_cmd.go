package main

import "github.com/hyprsh/tnl/internal/api"

var zoneGroup = newGroup("zone")

func init() {
	// CERTIFICATE comes in slice 10.
	zoneGroup.subs["ls"] = lsCommand("zone ls", api.PathZones, "ZONE\tMODE\tPROVIDER", func(l api.ZoneList) (rows [][]string) {
		for _, z := range l.Zones {
			rows = append(rows, []string{z.Name, z.Mode, orDash(z.Provider)})
		}
		return rows
	})
}
