//go:build !outpost

package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/render"
)

var outpostGroup = newGroup("outpost")

func init() {
	outpostGroup.subs["add"] = outpostAdd
	outpostGroup.subs["rm"] = outpostRm
	outpostGroup.subs["rekey"] = outpostRekey
	outpostGroup.subs["ls"] = lsCommand("outpost ls", api.PathOutposts, render.Outposts)
}

// outpostAdd prints the join token on the first line of stdout, so it can be
// captured, then two install lines pinned to the Edge's version: the second for
// Linux (as root), the third for macOS (as the user).
func outpostAdd(args []string) error {
	c := newClientCommand("outpost add")
	ttl := c.fs.String("ttl", api.DefaultTokenTTL, "how long the join token works, such as 30m or 2h")
	pos, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErr("outpost add", "usage: tnl outpost add <name> [--ttl D]")
	}
	r, err := api.Call[api.OutpostAddReply](context.Background(), c.client(), http.MethodPost, api.PathOutposts, api.OutpostAddRequest{Name: pos[0], TTL: *ttl})
	if err != nil {
		return err
	}
	printJoin(r.Token)
	return nil
}

// printJoin prints the token, then the Linux and macOS install lines that carry
// it. The Edge does not know the Outpost's OS. A build without a release
// version cannot pin, and says so on stderr.
func printJoin(token string) {
	v, _ := versionInfo()
	fmt.Fprintln(stdout, token)
	fmt.Fprintln(stdout, installLine(v, token, true))
	fmt.Fprintln(stdout, installLine(v, token, false))
	fmt.Fprintln(stderr, "note: run the first install line on Linux and the second on macOS")
	if v == "dev" {
		fmt.Fprintln(stderr, "note: this build has no release version, so the install lines are not pinned: they install the latest release")
	}
}

// installLine is the command an Outpost host runs: with sudo for the systemd
// install on Linux, without for the per-user install on macOS. A release
// version pins the line to that release's install.sh; "dev" cannot pin and
// takes the latest.
func installLine(version, token string, sudo bool) string {
	base := "https://github.com/hyprsh/tnl/releases/latest/download/install.sh"
	if version != "dev" {
		base = "https://github.com/hyprsh/tnl/releases/download/v" + version + "/install.sh"
	}
	sh := "sh"
	if sudo {
		sh = "sudo sh"
	}
	return "curl -fsSL " + base + " | " + sh + " -s -- outpost --token " + token
}

// outpostRm removes an Outpost; --with-routes removes its Routes too.
func outpostRm(args []string) error {
	c := newClientCommand("outpost rm")
	with := c.fs.Bool("with-routes", false, "also remove the Routes through the Outpost")
	pos, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErr("outpost rm", "usage: tnl outpost rm <name> [--with-routes]")
	}
	r, err := api.Call[api.OutpostRmReply](context.Background(), c.client(), http.MethodPost, api.PathOutpostRm,
		api.OutpostRmRequest{Name: pos[0], WithRoutes: *with})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "removed Outpost %s\n", r.Name)
	if len(r.Routes) > 0 {
		fmt.Fprintf(stdout, "removed its Routes: %s\n", strings.Join(r.Routes, ", "))
	}
	printHandovers(r.Handovers)
	printWarnings(r.Warnings)
	return nil
}

// outpostRekey revokes the Outpost's key and prints a new token and install line.
func outpostRekey(args []string) error {
	c := newClientCommand("outpost rekey")
	ttl := c.fs.String("ttl", api.DefaultTokenTTL, "how long the join token works, such as 30m or 2h")
	pos, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErr("outpost rekey", "usage: tnl outpost rekey <name> [--ttl D]")
	}
	r, err := api.Call[api.OutpostAddReply](context.Background(), c.client(), http.MethodPost, api.PathOutpostRekey,
		api.OutpostRekeyRequest{Name: pos[0], TTL: *ttl})
	if err != nil {
		return err
	}
	printJoin(r.Token)
	return nil
}
