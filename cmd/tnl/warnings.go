//go:build !outpost

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/hyprsh/tnl/internal/api"
)

// stderr is where warnings go; tests swap it.
var stderr io.Writer = os.Stderr

// printWarnings prints each warning of a reply to stderr. A command with
// warnings still exits 0. Every command whose reply embeds api.Warnings calls it.
func printWarnings(w api.Warnings) {
	for _, m := range w.Warnings {
		fmt.Fprintf(stderr, "warning: %s\n", m)
	}
}
