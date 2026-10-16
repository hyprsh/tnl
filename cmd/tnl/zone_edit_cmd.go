//go:build !outpost

package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/hyprsh/tnl/internal/api"
)

func init() {
	zoneGroup.subs["edit"] = zoneEdit
}

const zoneEditUsage = "usage: tnl zone edit <zone> (--wildcard PROVIDER | --per-hostname | --token)"

// zoneEdit switches a Zone between per-Hostname and wildcard, or replaces a
// wildcard Zone's token. The token comes from a hidden prompt on a terminal,
// else from stdin, as in `zone add`.
func zoneEdit(args []string) error {
	c := newClientCommand("zone edit")
	provider := c.fs.String("wildcard", "", "switch to one wildcard certificate through this DNS provider (cloudflare); the token is read from the terminal or stdin")
	perHostname := c.fs.Bool("per-hostname", false, "switch to a certificate for each Hostname, and delete the token")
	token := c.fs.Bool("token", false, "replace the DNS token, read from the terminal or stdin")
	pos, err := c.parse(args)
	if err != nil {
		return err
	}
	chosen := 0
	for _, on := range []bool{*provider != "", *perHostname, *token} {
		if on {
			chosen++
		}
	}
	if len(pos) != 1 || chosen != 1 {
		return fmt.Errorf("%s", zoneEditUsage)
	}
	req := api.ZoneEditRequest{Name: pos[0], Wildcard: *provider, PerHostname: *perHostname}
	switch {
	case *provider != "":
		if req.Token, err = readToken(*provider); err != nil {
			return err
		}
	case *token:
		if req.Token, err = readToken("DNS"); err != nil {
			return err
		}
	}
	z, err := api.Call[api.ZoneEditReply](context.Background(), c.client(), http.MethodPost, api.PathZoneEdit, req)
	if err != nil {
		return err
	}
	if z.DNSZone != "" {
		fmt.Fprintf(stdout, "DNS zone %s\n", z.DNSZone)
	}
	if *token {
		fmt.Fprintf(stdout, "replaced the token of Zone %s\n", z.Name)
		return nil
	}
	if z.Provider != "" {
		fmt.Fprintf(stdout, "Zone %s is now %s (%s)\n", z.Name, z.Mode, z.Provider)
	} else {
		fmt.Fprintf(stdout, "Zone %s is now %s\n", z.Name, z.Mode)
	}
	if len(z.Covers) > 0 {
		fmt.Fprintf(stdout, "the certificate covers %s\n", strings.Join(z.Covers, ", "))
	}
	printWarnings(z.Warnings)
	return nil
}
