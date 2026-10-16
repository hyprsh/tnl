package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hyprsh/tnl/internal/api"
)

func init() {
	routeGroup.subs["add"] = routeAdd
}

// checkOutpostFlag applies the role from info: an Outpost's Routes always go
// through itself, so `--outpost` doesn't exist there, and on the Edge host
// `route add` needs it.
func checkOutpostFlag(cl *api.Client, command string, given, empty bool, service string) error {
	info, err := cl.Info(context.Background())
	if err != nil {
		return err
	}
	switch {
	case info.Role == api.RoleOutpost && given:
		return fmt.Errorf("--outpost doesn't exist on an Outpost: `tnl %s` here always goes through this Outpost", command)
	case info.Role != api.RoleOutpost && command == "route add" && empty:
		return fmt.Errorf("--outpost is required on the Edge host: which Outpost's network is %s on?", service)
	}
	return nil
}

// routeAdd saves a Route and swaps it in. The test dial and the certificate wait
// come in later slices.
func routeAdd(args []string) error {
	c := newClientCommand("route add")
	outpost := c.fs.String("outpost", "", "the Outpost whose network the Service is on (required on the Edge host, absent on an Outpost)")
	hostHeader := c.fs.String("host-header", "", "the Host header to send to the Service, instead of the Visitor's")
	insecure := c.fs.Bool("insecure-skip-verify", false, "skip verifying an https:// Service's certificate")
	pos, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return fmt.Errorf("usage: tnl route add <hostname> <service-url> --outpost NAME")
	}
	cl := c.client()
	if err := checkOutpostFlag(cl, "route add", flagGiven(c.fs, "outpost"), *outpost == "", pos[1]); err != nil {
		return err
	}
	r, err := api.Call[api.RouteAddReply](context.Background(), cl, http.MethodPost, api.PathRoutes,
		api.RouteAddRequest{Hostname: pos[0], Service: pos[1], Outpost: *outpost,
			HostHeader: *hostHeader, InsecureSkipVerify: *insecure})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "added Route %s to %s through Outpost %s\n", r.Hostname, r.Service, r.Outpost)
	printWarnings(r.Warnings)
	return nil
}
