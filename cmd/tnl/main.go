// Command tnl is the Edge, the Outpost and the CLI in one binary. A build with
// the outpost tag leaves out the Edge and everything that links Caddy.
package main

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/hyprsh/tnl/internal/logx"
)

// exitConfig is EX_CONFIG: a flag, variable or value the operator must fix.
const exitConfig = 78

// configError marks an error as a configuration problem, which exits 78.
type configError struct{ error }

func (e configError) Unwrap() error { return e.error }

func exitCode(err error) int {
	var ce configError
	switch {
	case err == nil:
		return 0
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
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	run, ok := commands[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "tnl: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err := run(os.Args[2:]); err != nil {
		var logged *logx.LoggedError
		if !errors.As(err, &logged) { // already logged at error
			fmt.Fprintf(os.Stderr, "tnl: %v\n", err)
		}
		os.Exit(exitCode(err))
	}
}

func usage() {
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintf(os.Stderr, "usage: tnl <command>\ncommands: %v\n", names)
}
