//go:build !outpost

package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/hyprsh/tnl/internal/keys"
)

// keygen and credential are full-build commands that run offline: they never
// contact an Edge.
func init() {
	commands["keygen"] = runKeygen
}

// runKeygen prints a new WireGuard private key in `wg genkey` format, for the
// Edge's --key-file.
func runKeygen(args []string) error {
	fs := flag.NewFlagSet("tnl keygen", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("keygen takes no arguments, got %q", fs.Arg(0))
	}
	priv, _, err := keys.Generate()
	if err != nil {
		return err
	}
	text, _ := priv.MarshalText()
	_, err = fmt.Fprintf(stdout, "%s\n", text)
	return err
}
