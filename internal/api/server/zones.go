package server

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/dnsname"
	"github.com/hyprsh/tnl/internal/state"
)

func zoneInfo(z state.Zone) api.ZoneInfo {
	return api.ZoneInfo{Name: z.Name, Mode: string(z.Mode), Provider: z.Provider} // never the token
}

func (s *Server) zoneList(_ context.Context, _ api.Actor, _ struct{}) (api.ZoneList, error) {
	out := api.ZoneList{Zones: []api.ZoneInfo{}}
	for _, z := range s.cfg.Store.Snapshot().Zones {
		out.Zones = append(out.Zones, zoneInfo(z))
	}
	sort.Slice(out.Zones, func(i, j int) bool { return out.Zones[i].Name < out.Zones[j].Name })
	return out, nil
}

func (s *Server) zoneAdd(ctx context.Context, actor api.Actor, req api.ZoneAddRequest) (api.ZoneAddReply, error) {
	if !actor.Local() {
		return api.ZoneAddReply{}, api.Errorf(http.StatusForbidden, "%s may not add Zones; run `tnl zone add` on the Edge host", actor)
	}
	name, err := zoneName(req.Name)
	if err != nil {
		return api.ZoneAddReply{}, err
	}
	z := state.Zone{Name: name, Mode: state.PerHostname}
	err = s.cfg.Store.Update(actor, func(st *state.State) error {
		for _, have := range st.Zones {
			if have.Name == name {
				return api.Errorf(http.StatusConflict, "Zone %s already exists", name)
			}
		}
		st.Zones = append(st.Zones, z)
		return nil
	})
	if err != nil {
		return api.ZoneAddReply{}, err
	}
	s.cfg.Log.Info("zone added", "actor", actor.String(), "zone", z.Name, "mode", z.Mode)
	// After the lock: a slow resolver must not delay another change.
	warnings := s.zoneDNSWarnings(ctx, z.Name)
	s.warn(actor, warnings, "zone", z.Name)
	return api.ZoneAddReply{ZoneInfo: zoneInfo(z), Warnings: api.Warnings{Warnings: warnings}}, nil
}

// zoneName normalises a Zone name (lower case, no trailing dot) and refuses
// anything that isn't a plain DNS name.
func zoneName(in string) (string, error) {
	return dnsName("Zone", in, "write the Zone's name without a wildcard; a wildcard certificate is a Zone option")
}

// dnsName is zoneName for any kind of name; noWildcard is the advice given when
// the name contains a wildcard.
func dnsName(kind, in, noWildcard string) (string, error) {
	name := dnsname.Normalize(in)
	switch {
	case name == "":
		return "", api.Errorf(http.StatusBadRequest, "the %s needs a name", kind)
	case strings.Contains(name, "*"):
		return "", api.Errorf(http.StatusBadRequest, "invalid %s name %q: %s", kind, in, noWildcard)
	}
	if err := dnsname.CheckName(name); err != nil {
		return "", api.Errorf(http.StatusBadRequest, "invalid %s name %q: %v", kind, in, err)
	}
	return name, nil
}
