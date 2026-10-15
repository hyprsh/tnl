//go:build outpost

package main

import "errors"

func init() {
	zoneGroup.subs["add"] = func([]string) error {
		return errors.New("this is the Outpost build; `tnl zone add` needs the full build, on the Edge host")
	}
}
