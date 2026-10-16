//go:build !outpost

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

// routeAdd saves a Route and swaps it in. The test dial and the certificate wait
// come in later slices.
func routeAdd(args []string) error {
	c := newClientCommand("route add")
	outpost := c.fs.String("outpost", "", "the Outpost whose network the Service is on (required on the Edge host)")
	hostHeader := c.fs.String("host-header", "", "the Host header to send to the Service, instead of the Visitor's")
	insecure := c.fs.Bool("insecure-skip-verify", false, "skip verifying an https:// Service's certificate")
	pos, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return fmt.Errorf("usage: tnl route add <hostname> <service-url> --outpost NAME")
	}
	if *outpost == "" {
		return fmt.Errorf("--outpost is required on the Edge host: which Outpost's network is %s on?", pos[1])
	}
	r, err := api.Call[api.RouteAddReply](context.Background(), c.client(), http.MethodPost, api.PathRoutes,
		api.RouteAddRequest{Hostname: pos[0], Service: pos[1], Outpost: *outpost,
			HostHeader: *hostHeader, InsecureSkipVerify: *insecure})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "added Route %s to %s through Outpost %s\n", r.Hostname, r.Service, r.Outpost)
	printWarnings(r.Warnings)
	return nil
}
