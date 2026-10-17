package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hyprsh/tnl/internal/api"
)

func init() {
	routeGroup.subs["edit"] = routeEdit
}

// routeEdit changes a Route and closes its open connections, even when only an
// option changed. A flag that is not given leaves its field alone, so
// `--host-header ""` clears the override and `--insecure-skip-verify=false`
// turns verification back on. `--header` replaces the whole list of extra
// headers and `--header ""` clears it.
func routeEdit(args []string) error {
	c := newClientCommand("route edit")
	service := c.fs.String("service", "", "the new Service URL")
	outpost := c.fs.String("outpost", "", "the Outpost whose network the Service is on")
	hostHeader := c.fs.String("host-header", "", "the Host header to send to the Service; \"\" sends the Visitor's")
	insecure := c.fs.Bool("insecure-skip-verify", false, "skip verifying an https:// Service's certificate")
	noHSTS := c.fs.Bool("no-hsts", false, "leave the Strict-Transport-Security header off this Route's replies")
	var headers lineFlag
	c.fs.Var(&headers, "header", "an extra response header, \"Name: value\"; repeatable")
	pos, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErr("route edit", "usage: tnl route edit <hostname> [--service URL] [--outpost NAME] [--host-header V] [--insecure-skip-verify=true|false] [--no-hsts=true|false] [--header 'Name: value']...")
	}
	cl := c.client()
	if err := checkOutpostFlag(cl, "route edit", flagGiven(c.fs, "outpost"), false, ""); err != nil {
		return err
	}
	req := api.RouteEditRequest{Hostname: pos[0]}
	if flagGiven(c.fs, "service") {
		req.Service = service
	}
	if flagGiven(c.fs, "outpost") {
		req.Outpost = outpost
	}
	if flagGiven(c.fs, "host-header") {
		req.HostHeader = hostHeader
	}
	if flagGiven(c.fs, "insecure-skip-verify") {
		req.InsecureSkipVerify = insecure
	}
	if flagGiven(c.fs, "no-hsts") {
		req.NoHSTS = noHSTS
	}
	if flagGiven(c.fs, "header") {
		list := []string(headers)
		if len(list) == 1 && list[0] == "" {
			list = []string{}
		}
		for _, h := range list {
			if h == "" {
				return usageErr("route edit", "--header \"\" clears the headers and can't be combined with another --header")
			}
		}
		req.Headers = &list
	}
	r, err := api.Call[api.RouteEditReply](context.Background(), cl, http.MethodPost, api.PathRouteEdit, req)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "edited Route %s: %s through Outpost %s\n", r.Hostname, r.Service, r.Outpost)
	printWarnings(r.Warnings)
	return nil
}
