package server

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/state"
)

// zoneRm removes the Zone. It is refused only if that would leave Routes
// outside every Zone; with WithRoutes those Routes go too. Routes a parent Zone
// still contains stay, and belong to it from then on. The connections of the
// Routes removed close after the store's lock is released, and the store's
// change publishers stop the certificates of every Hostname that left.
func (s *Server) zoneRm(_ context.Context, actor api.Actor, req api.ZoneRmRequest) (api.ZoneRmReply, error) {
	if !actor.Local() {
		return api.ZoneRmReply{}, api.Errorf(http.StatusForbidden, "%s may not remove Zones; run `tnl zone rm` on the Edge host", actor)
	}
	name, err := zoneName(req.Name)
	if err != nil {
		return api.ZoneRmReply{}, err
	}
	var removed, moved []string
	err = s.cfg.Store.Update(actor, func(st *state.State) error {
		i := slices.IndexFunc(st.Zones, func(z state.Zone) bool { return z.Name == name })
		if i < 0 {
			return api.Errorf(http.StatusNotFound, "no Zone %s", name)
		}
		stranded := st.RoutesStrandedBy(name)
		if len(stranded) > 0 && !req.WithRoutes {
			slices.Sort(stranded)
			return api.Errorf(http.StatusConflict, "Routes would be left outside every Zone: %s; remove them first, or pass --with-routes", strings.Join(stranded, ", "))
		}
		for _, r := range st.Routes {
			if z, in := st.ZoneOf(r.Hostname); in && z.Name == name && !slices.Contains(stranded, r.Hostname) {
				moved = append(moved, r.Hostname)
			}
		}
		removed = st.RemoveRoutes(func(r state.Route) bool { return slices.Contains(stranded, r.Hostname) })
		st.Zones = slices.Delete(slices.Clone(st.Zones), i, i+1)
		return nil
	})
	if err != nil {
		return api.ZoneRmReply{}, err
	}
	s.cfg.Log.Info("zone removed", "actor", actor.String(), "zone", name, "routes_removed", len(removed), "routes_moved", len(moved))
	if s.cfg.Conns != nil {
		for _, h := range removed {
			s.cfg.Conns.CloseRoute(h)
		}
	}
	slices.Sort(removed)
	slices.Sort(moved)
	return api.ZoneRmReply{Name: name, Removed: nonNil(removed), Moved: nonNil(moved)}, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
