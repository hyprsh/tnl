package main

import (
	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/render"
)

var routeGroup = newGroup("route")

func init() {
	routeGroup.subs["ls"] = lsCommand("route ls", api.PathRoutes, render.Routes)
}
