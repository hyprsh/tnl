package server

import (
	"context"
	"fmt"
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
	var dnsZone string
	wildcard := req.Provider != "" || req.Token != ""
	if wildcard {
		if dnsZone, err = s.checkWildcard(ctx, name, req); err != nil {
			return api.ZoneAddReply{}, err
		}
		z = state.Zone{Name: name, Mode: state.Wildcard, Provider: req.Provider, Token: req.Token}
	}
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
	s.cfg.Log.Info("zone added", "actor", actor.String(), "zone", z.Name, "mode", z.Mode, "provider", z.Provider) // never the token
	// After the lock: a slow resolver must not delay another change.
	var warnings []string
	reply := api.ZoneAddReply{ZoneInfo: zoneInfo(z), DNSZone: dnsZone}
	if wildcard {
		reply.Covers = []string{"*." + z.Name, z.Name}
		warnings = append(warnings, s.wildcardCertificate(ctx, z.Name)...)
	}
	warnings = append(warnings, s.zoneDNSWarnings(ctx, z.Name)...)
	s.warn(actor, warnings, "zone", z.Name)
	reply.Warnings = api.Warnings{Warnings: warnings}
	return reply, nil
}

// checkWildcard validates a wildcard Zone's request and runs its one hard
// gate: the token must be able to read the DNS zone. Nothing is saved before.
func (s *Server) checkWildcard(ctx context.Context, name string, req api.ZoneAddRequest) (dnsZone string, err error) {
	switch {
	case req.Provider == "":
		return "", api.Errorf(http.StatusBadRequest, "a DNS token needs its provider: pass --wildcard PROVIDER")
	case req.Token == "":
		return "", api.Errorf(http.StatusBadRequest, "the wildcard Zone needs the %s token: give it on stdin, or at the prompt on a terminal", req.Provider)
	case s.cfg.Wildcards == nil:
		return "", api.Errorf(http.StatusBadRequest, "this Edge cannot get wildcard certificates")
	}
	for _, have := range s.cfg.Store.Snapshot().Zones {
		if have.Name == name {
			return "", api.Errorf(http.StatusConflict, "Zone %s already exists", name)
		}
	}
	dnsZone, err = s.cfg.Wildcards.Check(ctx, req.Provider, req.Token, name)
	if err != nil {
		return "", api.Errorf(http.StatusBadRequest, "refusing Zone %s: %s", name, redact(err.Error(), req.Token))
	}
	return dnsZone, nil
}

// redact is the last line of defence: whatever produced msg, the token is not
// in what leaves.
func redact(msg, token string) string {
	if token == "" {
		return msg
	}
	return strings.ReplaceAll(msg, token, "[redacted]")
}

// wildcardCertificate reloads Caddy so the new Zone's policy takes effect, then
// waits for its certificate, up to CertWait. Failing only warns: Caddy keeps
// trying.
func (s *Server) wildcardCertificate(ctx context.Context, zone string) []string {
	if err := s.cfg.Wildcards.Reload(); err != nil {
		return []string{fmt.Sprintf("couldn't reload Caddy for the wildcard certificate of %s: %v", zone, err)}
	}
	wait := s.cfg.CertWait
	if wait <= 0 {
		wait = DefaultCertWait
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	// Caddy obtains one certificate per name, the wildcard's and the apex's.
	for _, name := range []string{"*." + zone, zone} {
		if err := s.cfg.Wildcards.Wait(ctx, name); err != nil {
			return []string{fmt.Sprintf("no certificate for %s yet: %v; Caddy keeps retrying", name, err)}
		}
	}
	return nil
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
