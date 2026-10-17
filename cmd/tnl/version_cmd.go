package main

import (
	"fmt"
	"strings"

	ver "github.com/hyprsh/tnl/internal/version"
)

// `tnl version` runs anywhere and contacts nothing. It prints the version, the
// build and one compatibility line.
func init() {
	commands["version"] = func(args []string) error {
		if len(args) > 0 {
			return usageErr("version", "version takes no arguments, got %q", strings.Join(args, " "))
		}
		fmt.Fprintf(stdout, "tnl %s %s\n%s\n", version, buildName, ver.CompatibilityLine(version, buildName))
		return nil
	}
}
