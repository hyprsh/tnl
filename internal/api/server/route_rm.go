package server

import (
	"context"
	"net/http"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/state"
)

// routeRm removes the Route and then closes its connections. The store's change
// publishers drop it from the router's table, so the next request is a 404; the
// close comes after Update returns, never under the store's lock.
func (s *Server) routeRm(_ context.Context, actor api.Actor, req api.RouteRmRequest) (api.RouteInfo, error) {
	if !actor.Local() {
		return api.RouteInfo{}, api.Errorf(http.StatusForbidden, "%s may not remove Routes here yet; run `tnl route rm` on the Edge host", actor)
	}
	host, err := dnsName("Hostname", req.Hostname, "a Route holds one exact Hostname")
	if err != nil {
		return api.RouteInfo{}, err
	}
	var gone state.Route
	err = s.cfg.Store.Update(actor, func(st *state.State) error {
		for i, have := range st.Routes {
			if have.Hostname == host {
				gone = have
				st.Routes = append(st.Routes[:i:i], st.Routes[i+1:]...)
				return nil
			}
		}
		return api.Errorf(http.StatusNotFound, "no Route for %s", host)
	})
	if err != nil {
		return api.RouteInfo{}, err
	}
	s.cfg.Log.Info("route removed", "actor", actor.String(), "hostname", gone.Hostname, "outpost", gone.Outpost, "service", gone.Service)
	if s.cfg.Conns != nil {
		s.cfg.Conns.CloseRoute(gone.Hostname)
	}
	return routeInfo(gone), nil
}
