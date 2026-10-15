//go:build outpost

package main

import "errors"

// An Outpost-only build manages no Outposts.
func init() {
	commands["outpost"] = func([]string) error {
		return errors.New("this is the Outpost build; `tnl outpost` needs the full build, on the Edge host")
	}
}
