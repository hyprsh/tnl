//go:build !outpost

package main

import "errors"

// Edge-only commands register from //go:build !outpost files like this one.
// The Edge itself arrives with the Caddy side.
func init() {
	commands["edge"] = func([]string) error { return errors.New("tnl edge is not built yet") }
}
