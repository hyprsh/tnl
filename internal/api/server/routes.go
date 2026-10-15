package server

import (
	"context"
	"net/http"
	"sort"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/state"
)

func routeInfo(r state.Route) api.RouteInfo {
	return api.RouteInfo{Hostname: r.Hostname, Outpost: r.Outpost, Service: r.Service}
}

func (s *Server) routeList(_ context.Context, _ api.Actor, _ struct{}) (api.RouteList, error) {
	out := api.RouteList{Routes: []api.RouteInfo{}}
	for _, r := range s.cfg.Store.Snapshot().Routes {
		out.Routes = append(out.Routes, routeInfo(r))
	}
	sort.Slice(out.Routes, func(i, j int) bool { return out.Routes[i].Hostname < out.Routes[j].Hostname })
	return out, nil
}

// routeAdd saves the Route. The store's change publishers swap it into the
// router's table, so the next request is served with no Caddy reload.
func (s *Server) routeAdd(_ context.Context, actor api.Actor, req api.RouteAddRequest) (api.RouteInfo, error) {
	if !actor.Local() {
		return api.RouteInfo{}, api.Errorf(http.StatusForbidden, "%s may not add Routes here yet; run `tnl route add` on the Edge host", actor)
	}
	host, err := dnsName("Hostname", req.Hostname, "a Route takes one exact Hostname; wildcard Routes are not supported")
	if err != nil {
		return api.RouteInfo{}, err
	}
	if req.Outpost == "" {
		return api.RouteInfo{}, api.Errorf(http.StatusBadRequest, "the Route needs an Outpost: pass --outpost NAME")
	}
	service, err := state.ParseService(req.Service)
	if err != nil {
		return api.RouteInfo{}, api.Errorf(http.StatusBadRequest, "%v", err)
	}
	r := state.Route{Hostname: host, Outpost: req.Outpost, Service: service.Canonical}
	err = s.cfg.Store.Update(actor.String(), func(st *state.State) error {
		for _, have := range st.Routes {
			if have.Hostname == host {
				return api.Errorf(http.StatusConflict, "%s is already held by a Route through Outpost %s", host, have.Outpost)
			}
		}
		known := false
		for _, o := range st.Outposts {
			known = known || o.Name == req.Outpost
		}
		if !known {
			return api.Errorf(http.StatusBadRequest, "unknown Outpost %q; add it first with `tnl outpost add %s`", req.Outpost, req.Outpost)
		}
		if !st.InZone(host) {
			return api.Errorf(http.StatusBadRequest, "%s is outside every Zone; add its Zone first with `tnl zone add`", host)
		}
		st.Routes = append(st.Routes, r)
		return nil
	})
	if err != nil {
		return api.RouteInfo{}, err
	}
	s.cfg.Log.Info("route added", "actor", actor.String(), "hostname", r.Hostname, "outpost", r.Outpost, "service", r.Service)
	return routeInfo(r), nil
}
