// Command tnl is the Edge, the Outpost and the CLI in one binary. A build with
// the outpost tag leaves out the Edge and everything that links Caddy.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/hyprsh/tnl/internal/logx"
)

// exitConfig is EX_CONFIG: a flag, variable or value the operator must fix.
const exitConfig = 78

// configError marks an error as a configuration problem, which exits 78.
type configError struct{ error }

func (e configError) Unwrap() error { return e.error }

func exitCode(err error) int {
	var ce configError
	var ee exitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		return ee.code
	case errors.As(err, &ce):
		return exitConfig
	default:
		return 1
	}
}

// commands maps a subcommand to its entry point. Each command file registers
// its own in init, so tickets that add commands touch disjoint files. Files
// that need Caddy carry //go:build !outpost.
var commands = map[string]func(args []string) error{}

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		var logged *logx.LoggedError
		if !errors.As(err, &logged) && !silent(err) { // already logged at error
			fmt.Fprintf(os.Stderr, "tnl: %v\n", err)
		}
		os.Exit(exitCode(err))
	}
}
