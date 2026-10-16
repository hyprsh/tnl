//go:build !outpost

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hyprsh/tnl/internal/credential"
	"github.com/hyprsh/tnl/internal/dnsname"
	"github.com/hyprsh/tnl/internal/settings"
)

func init() {
	commands["credential"] = runCredential
}

// runCredential mints an Outpost credential offline: it reads the Edge's private
// key, makes the Outpost's key pair, and prints the Edge's line for the Outpost
// and the credential. It never contacts an Edge.
func runCredential(args []string) error {
	fs := flag.NewFlagSet("tnl credential", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var address, keyFile string
	var port int
	fs.StringVar(&address, "address", "", "the Edge's --address, the DNS name Outposts dial (required)")
	fs.IntVar(&port, "wireguard-port", 51820, "the Edge's WireGuard port")
	fs.StringVar(&keyFile, "key-file", os.Getenv("TNL_KEY_FILE"), "the Edge's private key file (or TNL_KEY holding the key)")
	var names []string
	for {
		if err := fs.Parse(args); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		names = append(names, fs.Arg(0))
		args = fs.Args()[1:]
	}
	switch {
	case len(names) != 1:
		return fmt.Errorf("usage: tnl credential <name> --address A [--wireguard-port N] [--key-file F]")
	case address == "":
		return fmt.Errorf("--address is required: the DNS name Outposts dial")
	case port < 1 || port > 65535:
		return fmt.Errorf("--wireguard-port must be a port from 1 to 65535, got %d", port)
	}
	name := names[0]
	if err := dnsname.CheckLabel(name); err != nil {
		return fmt.Errorf("invalid Outpost name: %v", err)
	}
	edgeKey, _, err := settings.EdgeKey(settingName(fs, "key-file", "TNL_KEY_FILE"), keyFile, os.Getenv("TNL_KEY"))
	if err != nil {
		return err
	}
	if edgeKey == nil {
		return fmt.Errorf("the Edge's private key is needed: pass --key-file, or set TNL_KEY (make one with `tnl keygen`)")
	}
	cred, pub, err := credential.Mint(*edgeKey, name, address, port)
	if err != nil {
		return err
	}
	pubText, _ := pub.MarshalText()
	label := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	_, err = fmt.Fprintf(stdout, "TNL_OUTPOST_%s=%s %s\nTNL_CREDENTIAL=%s\n", label, name, pubText, cred.Encode())
	return err
}
