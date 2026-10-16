package main

import (
	"fmt"
	"strings"
)

// `tnl version` runs anywhere and contacts nothing. Its compatibility line
// comes in slice 7.
func init() {
	commands["version"] = func(args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("version takes no arguments, got %q", strings.Join(args, " "))
		}
		fmt.Fprintf(stdout, "tnl %s %s\n", version, buildName)
		return nil
	}
}
