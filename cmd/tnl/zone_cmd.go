package main

import (
	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/render"
)

var zoneGroup = newGroup("zone")

func init() {
	zoneGroup.subs["ls"] = lsCommand("zone ls", api.PathZones, render.Zones)
}
