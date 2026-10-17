//go:build !outpost

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/hyprsh/tnl/internal/api"
)

func init() {
	zoneGroup.subs["add"] = zoneAdd
}

// stdin is where a command reads a secret from when it is not a terminal;
// tests swap it.
var stdin io.Reader = os.Stdin

// zoneAdd saves a Zone. With --wildcard PROVIDER it gets one wildcard
// certificate through that DNS provider, and the token comes from a hidden
// prompt on a terminal, else from stdin: never from an argument or a variable.
func zoneAdd(args []string) error {
	c := newClientCommand("zone add")
	provider := c.fs.String("wildcard", "", "get one wildcard certificate through this DNS provider (cloudflare); the token is read from the terminal or stdin")
	pos, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErr("zone add", "usage: tnl zone add <zone> [--wildcard PROVIDER]")
	}
	req := api.ZoneAddRequest{Name: pos[0], Provider: *provider}
	if *provider != "" {
		if req.Token, err = readToken(*provider); err != nil {
			return err
		}
	}
	z, err := api.Call[api.ZoneAddReply](context.Background(), c.client(), http.MethodPost, api.PathZones, req)
	if err != nil {
		return err
	}
	printZoneResult(fmt.Sprintf("added Zone %s (%s)", z.Name, zoneMode(z.ZoneInfo)), z.WildcardInfo, z.Warnings)
	printHandovers(z.Handovers)
	if z.Mode == "per-hostname" {
		fmt.Fprintf(stderr, "note: each Hostname in %s needs its own DNS record, or a wildcard record, pointing at the Edge\n", z.Name)
	}
	return nil
}

// readToken reads the DNS token: hidden on a terminal, else all of stdin.
func readToken(provider string) (string, error) {
	var raw []byte
	var err error
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprintf(stderr, "%s API token (hidden): ", provider)
		raw, err = term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(stderr)
	} else {
		raw, err = io.ReadAll(io.LimitReader(stdin, 64<<10))
	}
	if err != nil {
		return "", fmt.Errorf("read the %s token: %w", provider, err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", errors.New("no token: give the " + provider + " token at the prompt, or pipe it on stdin")
	}
	return token, nil
}
