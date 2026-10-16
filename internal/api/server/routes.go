package server

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/state"
)

func routeInfo(r state.Route) api.RouteInfo {
	return api.RouteInfo{Hostname: r.Hostname, Outpost: r.Outpost, Service: r.Service, Options: r.Options()}
}

// afterRouteChange runs once a Route change is saved and the store's lock is
// released. The test dial, the certificate wait and the warnings plug in here.
// before is the Route as it was, nil for a new one.
func (s *Server) afterRouteChange(before *state.Route, after state.Route) {
	// A changed Route's open connections close, even when only an option
	// changed, so the next request meets the new Route.
	if before != nil && s.cfg.Conns != nil {
		s.cfg.Conns.CloseRoute(after.Hostname)
	}
}

// routeEdit changes the Route's Service, Outpost or options and closes its
// connections.
func (s *Server) routeEdit(_ context.Context, actor api.Actor, req api.RouteEditRequest) (api.RouteInfo, error) {
	if !actor.Local() {
		return api.RouteInfo{}, api.Errorf(http.StatusForbidden, "%s may not edit Routes here yet; run `tnl route edit` on the Edge host", actor)
	}
	host, err := dnsName("Hostname", req.Hostname, "a Route holds one exact Hostname")
	if err != nil {
		return api.RouteInfo{}, err
	}
	if req.Service == nil && req.Outpost == nil && req.HostHeader == nil && req.InsecureSkipVerify == nil {
		return api.RouteInfo{}, api.Errorf(http.StatusBadRequest, "nothing to change: pass --service, --outpost, --host-header or --insecure-skip-verify")
	}
	var service state.Service
	if req.Service != nil {
		if service, err = state.ParseService(*req.Service); err != nil {
			return api.RouteInfo{}, api.Errorf(http.StatusBadRequest, "%v", err)
		}
	}
	if req.HostHeader != nil {
		if err := state.CheckHostHeader(*req.HostHeader); err != nil {
			return api.RouteInfo{}, api.Errorf(http.StatusBadRequest, "%v", err)
		}
	}
	var before, after state.Route
	err = s.cfg.Store.Update(actor, func(st *state.State) error {
		for i, have := range st.Routes {
			if have.Hostname != host {
				continue
			}
			before, after = have, have
			if req.Service != nil {
				after.Service = service.Canonical
			}
			if req.Outpost != nil {
				if !hasOutpost(st, *req.Outpost) {
					return api.Errorf(http.StatusBadRequest, "unknown Outpost %q; add it first with `tnl outpost add %s`", *req.Outpost, *req.Outpost)
				}
				after.Outpost = *req.Outpost
			}
			if req.HostHeader != nil {
				after.HostHeader = *req.HostHeader
			}
			if req.InsecureSkipVerify != nil {
				after.InsecureSkipVerify = *req.InsecureSkipVerify
			}
			st.Routes = append([]state.Route(nil), st.Routes...)
			st.Routes[i] = after
			return nil
		}
		return api.Errorf(http.StatusNotFound, "no Route for %s", host)
	})
	if err != nil {
		return api.RouteInfo{}, err
	}
	s.cfg.Log.Info("route edited", "actor", actor.String(), "hostname", after.Hostname, "outpost", after.Outpost, "service", after.Service,
		"host_header", after.HostHeader, "insecure_skip_verify", after.InsecureSkipVerify)
	s.afterRouteChange(&before, after)
	return routeInfo(after), nil
}

func hasOutpost(st *state.State, name string) bool {
	for _, o := range st.Outposts {
		if o.Name == name {
			return true
		}
	}
	return false
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
func (s *Server) routeAdd(_ context.Context, actor api.Actor, req api.RouteAddRequest) (api.RouteAddReply, error) {
	if !actor.Local() {
		return api.RouteAddReply{}, api.Errorf(http.StatusForbidden, "%s may not add Routes here yet; run `tnl route add` on the Edge host", actor)
	}
	host, err := dnsName("Hostname", req.Hostname, "a Route takes one exact Hostname; wildcard Routes are not supported")
	if err != nil {
		return api.RouteAddReply{}, err
	}
	if req.Outpost == "" {
		return api.RouteAddReply{}, api.Errorf(http.StatusBadRequest, "the Route needs an Outpost: pass --outpost NAME")
	}
	service, err := state.ParseService(req.Service)
	if err != nil {
		return api.RouteAddReply{}, api.Errorf(http.StatusBadRequest, "%v", err)
	}
	if err := state.CheckHostHeader(req.HostHeader); err != nil {
		return api.RouteAddReply{}, api.Errorf(http.StatusBadRequest, "%v", err)
	}
	r := state.Route{Hostname: host, Outpost: req.Outpost, Service: service.Canonical,
		HostHeader: req.HostHeader, InsecureSkipVerify: req.InsecureSkipVerify}
	err = s.cfg.Store.Update(actor, func(st *state.State) error {
		for _, have := range st.Routes {
			if have.Hostname == host {
				return api.Errorf(http.StatusConflict, "%s is already held by a Route through Outpost %s", host, have.Outpost)
			}
		}
		if !hasOutpost(st, req.Outpost) {
			return api.Errorf(http.StatusBadRequest, "unknown Outpost %q; add it first with `tnl outpost add %s`", req.Outpost, req.Outpost)
		}
		if !st.InZone(host) {
			return api.Errorf(http.StatusBadRequest, "%s is outside every Zone; add its Zone first with `tnl zone add`", host)
		}
		st.Routes = append(st.Routes, r)
		return nil
	})
	if err != nil {
		return api.RouteAddReply{}, err
	}
	s.cfg.Log.Info("route added", "actor", actor.String(), "hostname", r.Hostname, "outpost", r.Outpost, "service", r.Service)
	s.afterRouteChange(nil, r)
	// After the lock, like every warning.
	var warnings []string
	if s.cfg.Liveness != nil && s.cfg.Liveness.State(r.Outpost) == liveness.Offline {
		warnings = append(warnings, fmt.Sprintf("Outpost %s is Offline: %s answers 503 until it is back", r.Outpost, r.Hostname))
	}
	s.warn(actor, warnings, "route", r.Hostname)
	return api.RouteAddReply{RouteInfo: routeInfo(r), Warnings: api.Warnings{Warnings: warnings}}, nil
}
