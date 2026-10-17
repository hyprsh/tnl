//go:build !outpost

package main

// buildName is the build this binary is: "full" or "outpost". It is what
// `tnl version`, the info reply and the start lines call the build.
const buildName = "full"

// refusedHere reports whether this build refuses the command (see the Outpost build).
func refusedHere(string) bool { return false }
