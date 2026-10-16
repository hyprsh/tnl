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

// printHandovers says which waiting lines the command turned into Routes, such
// as "newbox declared photos.example.com and now serves it". Every command
// whose reply embeds api.Handovers calls it.
func printHandovers(h api.Handovers) {
	for _, l := range h.Lines() {
		fmt.Fprintln(stdout, l)
	}
}
