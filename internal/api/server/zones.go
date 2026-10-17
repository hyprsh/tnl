package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/dnsname"
	"github.com/hyprsh/tnl/internal/remind"
	"github.com/hyprsh/tnl/internal/state"
)

func zoneInfo(z state.Zone) api.ZoneInfo {
	return api.ZoneInfo{Name: z.Name, Mode: string(z.Mode), Provider: z.Provider} // never the token
}

func (s *Server) zoneAdd(ctx context.Context, actor api.Actor, req api.ZoneAddRequest) (api.ZoneAddReply, error) {
	if err := localOnly(actor, "add Zones", "zone add"); err != nil {
		return api.ZoneAddReply{}, err
	}
	if err := s.zonesDeclared(); err != nil {
		return api.ZoneAddReply{}, err
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
	handed, err := s.cfg.Store.UpdateReport(actor, func(st *state.State) error {
		if _, have := st.Zone(name); have {
			return api.Errorf(http.StatusConflict, "Zone %s already exists", name)
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
	reply := api.ZoneAddReply{ZoneInfo: zoneInfo(z), Handovers: served(handed)}
	if wildcard {
		reply.WildcardInfo = wildcardInfo(dnsZone, z.Name)
		w := s.wildcardCertificate(ctx, z.Name)
		warnings = append(warnings, w...)
	}
	warnings = append(warnings, s.zoneDNSWarnings(ctx, z)...)
	s.warn(actor, warnings, "zone", z.Name)
	reply.Warnings = api.Warnings{Warnings: warnings}
	return reply, nil
}

func wildcardInfo(dnsZone, zone string) api.WildcardInfo {
	return api.WildcardInfo{DNSZone: dnsZone, Covers: dnsname.WildcardNames(zone)}
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
	if _, have := s.cfg.Store.Snapshot().Zone(name); have {
		return "", api.Errorf(http.StatusConflict, "Zone %s already exists", name)
	}
	return s.checkToken(ctx, req.Provider, req.Token, name)
}

// checkToken is the one hard gate of a wildcard Zone: the token must be able to
// read the DNS zone that holds it. It returns that DNS zone.
func (s *Server) checkToken(ctx context.Context, provider, token, zone string) (string, error) {
	dnsZone, err := s.cfg.Wildcards.Check(ctx, provider, token, zone)
	if err != nil {
		return "", api.Errorf(http.StatusBadRequest, "refusing Zone %s: %s", zone, redact(err.Error(), token))
	}
	return dnsZone, nil
}

// coveredRoutes lists the Hostnames of the Routes that the Zone holds and its
// wildcard certificate covers, in a state where the Zone is a wildcard.
func coveredRoutes(st *state.State, zone string) []string {
	var out []string
	for _, r := range st.Routes {
		if z, in := st.ZoneOf(r.Hostname); in && z.Name == zone && dnsname.WildcardCovers(r.Hostname, []string{zone}) {
			out = append(out, r.Hostname)
		}
	}
	return out
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
	ctx, cancel := context.WithTimeout(ctx, s.certWait())
	defer cancel()
	// Caddy obtains one certificate per name, the wildcard's and the apex's.
	for _, name := range dnsname.WildcardNames(zone) {
		if err := s.cfg.Wildcards.Wait(ctx, name); err != nil {
			return []string{fmt.Sprintf("no certificate for %s yet: %v; Caddy keeps retrying", name, err)}
		}
	}
	s.cfg.Wildcards.Settle()
	return nil
}

func (s *Server) certWait() time.Duration {
	if s.cfg.CertWait > 0 {
		return s.cfg.CertWait
	}
	return DefaultCertWait
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

// CheckDeclaredZones runs the checks of `zone add` for Zones the Edge saved at
// start, after the lock and without stopping anything: a wildcard token that
// can't read its DNS zone is logged at error and the Zone is kept. The check is
// repeated on the reminder schedule, with an error each time, until it passes;
// Caddy retries the certificate like any failed issuance meanwhile. The other
// checks warn as they do at `zone add`. It returns when ctx ends or every check
// has passed.
func (s *Server) CheckDeclaredZones(ctx context.Context, zones []state.Zone, sched remind.Schedule, clock remind.Clock) {
	var wg sync.WaitGroup
	for _, z := range zones {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var warnings []string
			if z.Mode == state.Wildcard {
				if s.cfg.Wildcards == nil || !s.declaredTokenPasses(ctx, z, sched, clock) {
					return
				}
				warnings = append(warnings, s.declaredCertificate(ctx, z.Name)...)
			}
			warnings = append(warnings, s.zoneDNSWarnings(ctx, z)...)
			s.warn(api.ActorLocal, warnings, "zone", z.Name)
		}()
	}
	wg.Wait()
}

// declaredTokenPasses checks the Zone's token, and while it fails logs an error
// and checks again whenever a reminder is due. It reports whether the token
// passed before ctx ended.
func (s *Server) declaredTokenPasses(ctx context.Context, z state.Zone, sched remind.Schedule, clock remind.Clock) bool {
	failed := func(err error, extra ...any) {
		// checkToken redacts the token from the message; redact again in case
		// the message came from elsewhere.
		s.cfg.Log.Error("the declared wildcard Zone's token check failed; the Zone is kept and its certificate is retried",
			append([]any{"zone", z.Name, "provider", z.Provider, "error", redact(err.Error(), z.Token)}, extra...)...)
	}
	_, err := s.checkToken(ctx, z.Provider, z.Token, z.Name)
	if err == nil {
		return true
	}
	if ctx.Err() != nil {
		return false
	}
	failed(err)
	if sched.Every == 0 && len(sched.After) == 0 {
		sched = remind.Standard
	}
	tracker := remind.NewTracker(sched, clock)
	tracker.Begin(z.Name)
	tick := time.Second
	if sched.Every > 0 && sched.Every < 4*time.Second {
		tick = sched.Every / 4
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
		}
		due := tracker.Due()
		if len(due) == 0 {
			continue
		}
		if _, err := s.checkToken(ctx, z.Provider, z.Token, z.Name); err != nil {
			if ctx.Err() == nil {
				failed(err, "failing_for", due[0].Since.Round(time.Second))
			}
			continue
		}
		since, _ := tracker.End(z.Name)
		s.cfg.Log.Info("the declared wildcard Zone's token check passes now", "zone", z.Name, "failed_for", since.Round(time.Second))
		return true
	}
}

// declaredCertificate waits, up to CertWait, for a declared wildcard Zone's
// certificate. Caddy's config already holds the Zone, so nothing is reloaded.
func (s *Server) declaredCertificate(ctx context.Context, zone string) []string {
	ctx, cancel := context.WithTimeout(ctx, s.certWait())
	defer cancel()
	for _, name := range dnsname.WildcardNames(zone) {
		if err := s.cfg.Wildcards.Wait(ctx, name); err != nil {
			return []string{fmt.Sprintf("no certificate for %s yet: %v; Caddy keeps retrying", name, err)}
		}
	}
	s.cfg.Wildcards.Settle()
	return nil
}
