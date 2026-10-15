//go:build outpost

package main

import "errors"

func init() {
	routeGroup.subs["add"] = func([]string) error {
		return errors.New("this is the Outpost build; `tnl route add` needs the full build, on the Edge host")
	}
}
