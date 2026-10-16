package server

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/state"
)

// zoneEdit switches a Zone between per-Hostname and wildcard, or replaces a
// wildcard Zone's token. A Visitor never sees a certificate error while it
// does: the certificates that served before are kept serving across the Caddy
// reload (Retire), and the new ones are waited for after it.
func (s *Server) zoneEdit(ctx context.Context, actor api.Actor, req api.ZoneEditRequest) (api.ZoneEditReply, error) {
	if err := localOnly(actor, "edit Zones", "zone edit"); err != nil {
		return api.ZoneEditReply{}, err
	}
	name, err := zoneName(req.Name)
	if err != nil {
		return api.ZoneEditReply{}, err
	}
	asked := 0
	for _, on := range []bool{req.Wildcard != "", req.PerHostname, req.Wildcard == "" && !req.PerHostname && req.Token != ""} {
		if on {
			asked++
		}
	}
	switch {
	case asked != 1:
		return api.ZoneEditReply{}, api.Errorf(http.StatusBadRequest, "say what to change: --wildcard PROVIDER, --per-hostname or --token, and only one of them")
	case req.PerHostname && req.Token != "":
		return api.ZoneEditReply{}, api.Errorf(http.StatusBadRequest, "--per-hostname takes no token")
	case s.cfg.Wildcards == nil:
		return api.ZoneEditReply{}, api.Errorf(http.StatusBadRequest, "this Edge cannot get wildcard certificates")
	}
	zone, ok := s.cfg.Store.Snapshot().Zone(name)
	if !ok {
		return api.ZoneEditReply{}, api.Errorf(http.StatusNotFound, "no Zone %s", name)
	}
	switch {
	case req.Wildcard != "":
		return s.zoneToWildcard(ctx, actor, zone, req)
	case req.PerHostname:
		return s.zoneToPerHostname(ctx, actor, zone)
	}
	return s.zoneToken(ctx, actor, zone, req.Token)
}

// changeZone applies fn to the Zone under the store's lock, refusing if it
// stopped being in the mode the checks ran against while they ran.
func (s *Server) changeZone(actor api.Actor, name string, from state.Mode, fn func(z *state.Zone)) error {
	return s.cfg.Store.Update(actor, func(st *state.State) error {
		i := slices.IndexFunc(st.Zones, func(z state.Zone) bool { return z.Name == name })
		if i < 0 {
			return api.Errorf(http.StatusNotFound, "no Zone %s", name)
		}
		if st.Zones[i].Mode != from {
			return api.Errorf(http.StatusConflict, "Zone %s changed while it was being edited; try again", name)
		}
		fn(&st.Zones[i])
		return nil
	})
}

func (s *Server) zoneToWildcard(ctx context.Context, actor api.Actor, zone state.Zone, req api.ZoneEditRequest) (api.ZoneEditReply, error) {
	switch {
	case zone.Mode == state.Wildcard:
		return api.ZoneEditReply{}, api.Errorf(http.StatusConflict, "Zone %s is already a wildcard Zone through %s; --token replaces its token", zone.Name, zone.Provider)
	case req.Token == "":
		return api.ZoneEditReply{}, api.Errorf(http.StatusBadRequest, "the wildcard Zone needs the %s token: give it on stdin, or at the prompt on a terminal", req.Wildcard)
	}
	dnsZone, err := s.checkToken(ctx, req.Wildcard, req.Token, zone.Name)
	if err != nil {
		return api.ZoneEditReply{}, err
	}
	wild := state.Zone{Name: zone.Name, Mode: state.Wildcard, Provider: req.Wildcard, Token: req.Token}
	// The Routes the wildcard takes over keep renewing their own certificates
	// until the wildcard's is in.
	if err := s.changeZone(actor, zone.Name, state.PerHostname, func(z *state.Zone) { *z = wild }); err != nil {
		return api.ZoneEditReply{}, err
	}
	s.cfg.Log.Info("zone edited", "actor", actor.String(), "zone", zone.Name, "mode", wild.Mode, "provider", wild.Provider) // never the token
	warnings := s.wildcardCertificate(ctx, zone.Name)
	s.warn(actor, warnings, "zone", zone.Name)
	return api.ZoneEditReply{
		ZoneInfo: zoneInfo(wild), WildcardInfo: wildcardInfo(dnsZone, zone.Name),
		Warnings: api.Warnings{Warnings: warnings},
	}, nil
}

func (s *Server) zoneToPerHostname(ctx context.Context, actor api.Actor, zone state.Zone) (api.ZoneEditReply, error) {
	if zone.Mode != state.Wildcard {
		return api.ZoneEditReply{}, api.Errorf(http.StatusConflict, "Zone %s is already a per-Hostname Zone", zone.Name)
	}
	covered := coveredRoutes(s.cfg.Store.Snapshot(), zone.Name)
	// The wildcard keeps serving every Hostname that has no certificate of its
	// own yet, through the reload and after it, until it expires: the Edge
	// loads the certificates of per-Hostname Zones' wildcards from storage.
	if err := s.changeZone(actor, zone.Name, state.Wildcard, func(z *state.Zone) {
		*z = state.Zone{Name: zone.Name, Mode: state.PerHostname} // the token goes with the mode
	}); err != nil {
		return api.ZoneEditReply{}, err
	}
	s.cfg.Log.Info("zone edited", "actor", actor.String(), "zone", zone.Name, "mode", state.PerHostname)
	// The state no longer holds the Zone's wildcard, so the reload drops its
	// policy; the Routes' own certificates are obtained after it.
	var warnings []string
	if err := s.cfg.Wildcards.Reload(); err != nil {
		warnings = append(warnings, fmt.Sprintf("couldn't reload Caddy after dropping the wildcard of %s: %v", zone.Name, err))
	} else {
		warnings = s.ownCertificates(ctx, covered)
	}
	s.warn(actor, warnings, "zone", zone.Name)
	return api.ZoneEditReply{
		ZoneInfo: zoneInfo(state.Zone{Name: zone.Name, Mode: state.PerHostname}),
		Warnings: api.Warnings{Warnings: warnings},
	}, nil
}

// ownCertificates waits, up to CertWait, for each Hostname to hold a
// certificate of its own. Failing only warns: Caddy keeps trying, and the
// wildcard keeps serving until it expires.
func (s *Server) ownCertificates(ctx context.Context, hostnames []string) []string {
	ctx, cancel := context.WithTimeout(ctx, s.certWait())
	defer cancel()
	var warnings []string
	for _, h := range hostnames {
		if err := s.cfg.Wildcards.WaitOwn(ctx, h); err != nil {
			warnings = append(warnings, fmt.Sprintf("no certificate of its own for %s yet: %v; the wildcard keeps serving it until it expires, and Caddy keeps retrying", h, err))
		}
	}
	return warnings
}

func (s *Server) zoneToken(ctx context.Context, actor api.Actor, zone state.Zone, token string) (api.ZoneEditReply, error) {
	if zone.Mode != state.Wildcard {
		return api.ZoneEditReply{}, api.Errorf(http.StatusConflict, "Zone %s has no token: it is a per-Hostname Zone", zone.Name)
	}
	dnsZone, err := s.checkToken(ctx, zone.Provider, token, zone.Name)
	if err != nil {
		return api.ZoneEditReply{}, err
	}
	// The DNS provider module reads the token from the state when a challenge
	// runs, so there is nothing to reload.
	if err := s.changeZone(actor, zone.Name, state.Wildcard, func(z *state.Zone) { z.Token = token }); err != nil {
		return api.ZoneEditReply{}, err
	}
	s.cfg.Log.Info("zone token replaced", "actor", actor.String(), "zone", zone.Name, "provider", zone.Provider) // never the token
	zone.Token = ""
	return api.ZoneEditReply{ZoneInfo: zoneInfo(zone), WildcardInfo: api.WildcardInfo{DNSZone: dnsZone}}, nil
}
