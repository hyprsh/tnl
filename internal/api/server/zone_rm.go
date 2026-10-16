package server

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/dnsname"
	"github.com/hyprsh/tnl/internal/state"
)

// zoneRm removes the Zone. It is refused only if that would leave Routes
// outside every Zone; with WithRoutes those Routes go too. Routes a parent Zone
// still contains stay, and belong to it from then on. The connections of the
// Routes removed close after the store's lock is released, and the store's
// change publishers stop the certificates of every Hostname that left.
func (s *Server) zoneRm(ctx context.Context, actor api.Actor, req api.ZoneRmRequest) (api.ZoneRmReply, error) {
	if err := localOnly(actor, "remove Zones", "zone rm"); err != nil {
		return api.ZoneRmReply{}, err
	}
	name, err := zoneName(req.Name)
	if err != nil {
		return api.ZoneRmReply{}, err
	}
	var removed, moved, waiting []string
	var wasWildcard bool
	handed, err := s.cfg.Store.UpdateReport(actor, func(st *state.State) error {
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
		for _, r := range st.Routes {
			if slices.Contains(stranded, r.Hostname) && st.Declares(r.Outpost) {
				waiting = append(waiting, r.Hostname)
			}
		}
		wasWildcard = st.Zones[i].Mode == state.Wildcard
		removed = st.RemoveRoutes(func(r state.Route) bool { return slices.Contains(stranded, r.Hostname) })
		st.Zones = slices.Delete(slices.Clone(st.Zones), i, i+1)
		return nil
	})
	if err != nil {
		return api.ZoneRmReply{}, err
	}
	s.cfg.Log.Info("zone removed", "actor", actor.String(), "zone", name, "routes_removed", len(removed), "routes_moved", len(moved), "routes_waiting", len(waiting))
	if s.cfg.Conns != nil {
		for _, h := range removed {
			s.cfg.Conns.CloseRoute(h)
		}
	}
	var warnings []string
	if wasWildcard && s.cfg.Wildcards != nil {
		// The Zone's policy and its token are gone from the state; Caddy stops
		// managing its certificate with the reload.
		if err := s.cfg.Wildcards.Reload(); err != nil {
			s.cfg.Log.Error("could not reload Caddy after removing a wildcard Zone", "zone", name, "error", err)
			warnings = append(warnings, fmt.Sprintf("couldn't reload Caddy after removing the wildcard Zone %s: %v", name, err))
		} else {
			// The moved Routes the parent's wildcard does not cover need
			// certificates of their own.
			now := s.cfg.Store.Snapshot()
			warnings = s.ownCertificates(ctx, slices.DeleteFunc(slices.Clone(moved), func(h string) bool {
				return dnsname.WildcardCovers(h, now.WildcardZones())
			}))
		}
	}
	s.warn(actor, warnings, "zone", name)
	slices.Sort(removed)
	slices.Sort(moved)
	return api.ZoneRmReply{Name: name, Removed: nonNil(removed), Moved: nonNil(moved), Waiting: waiting, Handovers: served(handed), Warnings: api.Warnings{Warnings: warnings}}, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
