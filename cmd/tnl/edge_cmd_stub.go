//go:build outpost

package main

import "errors"

// An Outpost-only build has no Edge and links no Caddy.
func init() {
	commands["edge"] = func([]string) error { return errors.New("this build is Outpost-only; it has no Edge") }
}
