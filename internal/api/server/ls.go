package server

import (
	"context"
	"sort"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/dnsname"
	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/state"
)

// The `ls` handlers read the Edge's runtime facts where they live (liveness,
// the certificate manager) and the state, and put them in api's row types.
// Nothing here decides a fact again: it only chooses the words.

// CertState is what the certificate manager knows about one name.
type CertState struct {
	// Managed is false when there is no manager (no web side), which knows
	// nothing: the name reads as obtaining.
	Managed bool
	// NotAfter is when the certificate held expires; zero when none is.
	NotAfter time.Time
	// Failures counts failed attempts since the last success.
	Failures     int
	FirstFailure time.Time
	LastError    string
}

// CertReader is the certificate manager's in-memory status as the `ls`
// handlers read it.
type CertReader interface {
	// Certificate is the status of the name's certificate.
	Certificate(name string) CertState
	// Covered says whether a held wildcard certificate covers the Hostname,
	// so it has none of its own.
	Covered(host string) bool
	// Now is the manager's clock.
	Now() time.Time
}

func (s *Server) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now()
	}
	return time.Now()
}

func seconds(d time.Duration) *int64 {
	n := int64(d / time.Second)
	return &n
}

// outpostState is the Outpost's state in the spec's words, with what liveness
// knows of it. An Outpost with a join token not yet used is `not joined`. One
// the liveness has never heard of and not seeded, which can only be a declared
// Outpost added while the Edge runs, reads as offline until it is heard.
func (s *Server) outpostState(o state.Outpost) (string, liveness.Info) {
	if o.OneTimeKey != nil {
		return api.StateNotJoined, liveness.Info{}
	}
	if s.cfg.Liveness == nil {
		return api.StateOnline, liveness.Info{}
	}
	info := s.cfg.Liveness.Info(o.Name)
	switch info.State {
	case liveness.Online:
		return api.StateOnline, info
	case liveness.Incompatible:
		return api.StateIncompatible, info
	}
	return api.StateOffline, info
}

func (s *Server) outpostList(_ context.Context, actor api.Actor, _ struct{}) (api.OutpostList, error) {
	if err := localOnly(actor, "list Outposts", "outpost ls"); err != nil {
		return api.OutpostList{}, err
	}
	st := s.cfg.Store.Snapshot()
	now := s.now()
	out := api.OutpostList{Outposts: []api.OutpostInfo{}}
	for _, o := range st.Outposts {
		stateName, info := s.outpostState(o)
		row := api.OutpostInfo{
			Name: o.Name, State: stateName, TunnelAddress: o.Address.String(),
			Routes: len(st.RoutesVia(o.Name)), DeclaredLines: len(st.DeclarationOf(o.Name)), Declared: o.Declared,
		}
		if o.OneTimeKey != nil {
			exp := o.TokenExpires.UTC()
			row.TokenExpires, row.TokenExpiresInSeconds = &exp, seconds(exp.Sub(now))
		}
		if info.Heard {
			seen := info.LastSeen.UTC()
			row.LastSeen, row.LastSeenAgoSeconds = &seen, seconds(info.Silent)
		}
		if info.Endpoint.IsValid() {
			row.Endpoint = info.Endpoint.String()
		}
		row.Version = info.Version
		out.Outposts = append(out.Outposts, row)
	}
	sort.Slice(out.Outposts, func(i, j int) bool { return out.Outposts[i].Name < out.Outposts[j].Name })
	return out, nil
}

// routeStatus is the first problem with the Route, in the spec's order.
func (s *Server) routeStatus(st *state.State, r state.Route, now time.Time) string {
	for _, o := range st.Outposts {
		if o.Name != r.Outpost {
			continue
		}
		switch name, _ := s.outpostState(o); name {
		case api.StateNotJoined:
			return api.StatusOutpostNotJoined
		case api.StateOffline:
			return api.StatusOutpostOffline
		case api.StateIncompatible:
			return api.StatusOutpostIncompat
		}
	}
	certs := s.cfg.CertReader
	if certs == nil || certs.Covered(r.Hostname) {
		return api.StatusOK
	}
	cs := certs.Certificate(r.Hostname)
	switch {
	case !cs.Managed:
		return api.StatusOK // nothing to tell: there is no web side
	case cs.NotAfter.IsZero():
		return api.StatusNoCertificate
	case !cs.NotAfter.After(now):
		return api.StatusCertExpired
	case cs.Failures > 0:
		return api.StatusRenewalFailing
	}
	return api.StatusOK
}

func (s *Server) routeList(_ context.Context, _ api.Actor, _ struct{}) (api.RouteList, error) {
	st := s.cfg.Store.Snapshot()
	now := s.now()
	out := api.RouteList{Routes: []api.RouteRow{}}
	for _, r := range st.Routes {
		out.Routes = append(out.Routes, api.RouteRow{RouteInfo: routeInfo(r), Status: s.routeStatus(st, r, now)})
	}
	for _, d := range st.Declarations {
		for _, l := range d.Lines {
			held, isHeld := st.Route(l.Hostname)
			if isHeld && held.Outpost == d.Outpost {
				continue // it is a Route, already listed
			}
			var why string
			switch {
			case isHeld:
				why = api.WaitingTakenBy(held.Outpost)
			case !st.InZone(l.Hostname):
				why = api.WaitingOutsideEveryZone
			default:
				why = api.WaitingNotAppliedYet
			}
			line := state.Route{Hostname: l.Hostname, Outpost: d.Outpost, Service: l.Service, HostHeader: l.HostHeader, InsecureSkipVerify: l.InsecureSkipVerify}
			out.Routes = append(out.Routes, api.RouteRow{RouteInfo: routeInfo(line), Status: api.StatusWaitingPrefix + why, Waiting: why})
		}
	}
	// A move shows its Hostname twice: the Route, then the line that waits.
	sort.SliceStable(out.Routes, func(i, j int) bool {
		a, b := out.Routes[i], out.Routes[j]
		if a.Hostname != b.Hostname {
			return a.Hostname < b.Hostname
		}
		if (a.Waiting == "") != (b.Waiting == "") {
			return a.Waiting == ""
		}
		return a.Outpost < b.Outpost
	})
	return out, nil
}

// certInfo puts a certificate's status in words.
func certInfo(name string, cs CertState, now time.Time) api.CertInfo {
	c := api.CertInfo{Name: name, Error: cs.LastError}
	if !cs.NotAfter.IsZero() {
		exp := cs.NotAfter.UTC()
		c.Expires = &exp
	}
	if cs.Failures > 0 && !cs.FirstFailure.IsZero() {
		c.FailingForSeconds = seconds(max(now.Sub(cs.FirstFailure), 0))
	}
	switch {
	case !cs.Managed || (cs.NotAfter.IsZero() && cs.Failures == 0):
		c.State = api.CertObtaining
	case cs.NotAfter.IsZero():
		c.State = api.CertFailed
	case !cs.NotAfter.After(now):
		c.State = api.CertExpired
	case cs.Failures > 0:
		c.State = api.CertRenewalFailing
	default:
		c.State = api.CertValid
	}
	return c
}

func (s *Server) zoneList(_ context.Context, _ api.Actor, _ struct{}) (api.ZoneList, error) {
	st := s.cfg.Store.Snapshot()
	certs := s.cfg.CertReader
	out := api.ZoneList{Zones: []api.ZoneRow{}}
	for _, z := range st.Zones {
		row := api.ZoneRow{ZoneInfo: zoneInfo(z), Declared: z.Declared}
		switch {
		case z.Mode == state.Wildcard:
			name := dnsname.WildcardNames(z.Name)[0]
			c := api.CertInfo{Name: name, State: api.CertObtaining}
			if certs != nil {
				c = certInfo(name, certs.Certificate(name), certs.Now())
			}
			row.Certificate = &c
		case certs != nil:
			var hosts []string
			for _, r := range st.Routes {
				if in, ok := st.ZoneOf(r.Hostname); ok && in.Name == z.Name && !certs.Covered(r.Hostname) {
					hosts = append(hosts, r.Hostname)
				}
			}
			sort.Strings(hosts)
			for _, h := range hosts {
				row.Certificates = append(row.Certificates, certInfo(h, certs.Certificate(h), certs.Now()))
			}
		}
		out.Zones = append(out.Zones, row)
	}
	sort.Slice(out.Zones, func(i, j int) bool { return out.Zones[i].Name < out.Zones[j].Name })
	return out, nil
}
