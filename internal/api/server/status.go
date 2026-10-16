package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/render"
)

// status answers GET PathStatus: the Edge's summary and what needs attention.
// It reads the same facts the `ls` handlers do, by calling them, so the two
// never disagree on what an Outpost's state or a certificate's is.
func (s *Server) status(ctx context.Context, actor api.Actor, _ struct{}) (api.EdgeStatus, error) {
	if err := localOnly(actor, "read the Edge's status", "status"); err != nil {
		return api.EdgeStatus{}, err
	}
	st := s.cfg.Store.Snapshot()
	now := s.now()
	out := api.EdgeStatus{
		Role: api.RoleEdge, Version: s.cfg.Version, Build: s.cfg.Build, Revision: s.cfg.Revision,
		Address: s.cfg.Address, WireGuardPort: s.cfg.WireGuardPort, KeyFingerprint: st.PrivateKey.Public().Fingerprint(),
		StateDir: s.cfg.StateDir, LogLevel: s.cfg.LogLevel, AccessLog: s.cfg.AccessLog,
		Zones: len(st.Zones), Attention: []api.AttentionItem{},
	}
	if !s.cfg.StartedAt.IsZero() {
		out.Started = s.cfg.StartedAt.UTC()
		out.UptimeSeconds = *seconds(max(now.Sub(s.cfg.StartedAt), 0))
	}

	outposts, err := s.outpostList(ctx, actor, struct{}{})
	if err != nil {
		return out, err
	}
	for _, o := range outposts.Outposts {
		switch o.State {
		case api.StateOnline:
			out.Outposts.Online++
		case api.StateOffline:
			out.Outposts.Offline++
			out.Attention = append(out.Attention, api.AttentionItem{
				Kind: api.AttentionOutpostOffline, Subject: o.Name, Message: outpostProblem(o, "is offline"),
			})
		case api.StateIncompatible:
			out.Outposts.Incompatible++
			out.Attention = append(out.Attention, api.AttentionItem{
				Kind: api.AttentionOutpostIncompatible, Subject: o.Name, Message: outpostProblem(o, "is incompatible"),
			})
		case api.StateNotJoined:
			out.Outposts.NotJoined++
		}
	}

	routes, _ := s.routeList(ctx, actor, struct{}{})
	var waiting []api.AttentionItem
	for _, r := range routes.Routes {
		if r.Waiting == "" {
			out.Routes++
			continue
		}
		waiting = append(waiting, api.AttentionItem{
			Kind: api.AttentionLineWaiting, Subject: r.Hostname, Outpost: r.Outpost,
			Message: waitingLine(r),
		})
	}

	zones, _ := s.zoneList(ctx, actor, struct{}{})
	var certs []api.CertInfo
	for _, z := range zones.Zones {
		if z.Certificate != nil {
			certs = append(certs, *z.Certificate)
		}
		certs = append(certs, z.Certificates...)
	}
	for _, c := range certs {
		if c.Expires != nil && c.State != api.CertExpired {
			out.Certificates.Count++
			if out.Certificates.SoonestExpiry == nil || c.Expires.Before(*out.Certificates.SoonestExpiry) {
				exp := *c.Expires
				out.Certificates.SoonestExpiry = &exp
			}
		}
		switch {
		case c.Failing():
			out.Attention = append(out.Attention, api.AttentionItem{
				Kind: api.AttentionCertificateFailing, Subject: c.Name, Message: certProblem(c, now),
			})
		case c.Expires != nil && c.Expires.Sub(now) < api.CertExpirySoon:
			out.Attention = append(out.Attention, api.AttentionItem{
				Kind: api.AttentionCertificateExpiring, Subject: c.Name,
				Message: fmt.Sprintf("certificate %s expires in %s", c.Name, render.Span(*seconds(c.Expires.Sub(now)))),
			})
		}
	}
	out.Attention = append(out.Attention, waiting...)
	return out, nil
}

// outpostProblem is "Outpost nas is offline, last seen 4m ago, 3 Routes".
func outpostProblem(o api.OutpostInfo, what string) string {
	seen := "not heard from since the Edge started"
	if o.LastSeenAgoSeconds != nil {
		seen = "last seen " + render.Span(*o.LastSeenAgoSeconds) + " ago"
	}
	n := o.Routes
	routes := fmt.Sprintf("%d Routes", n)
	if n == 1 {
		routes = "1 Route"
	}
	return fmt.Sprintf("Outpost %s %s, %s, %s", o.Name, what, seen, routes)
}

func certProblem(c api.CertInfo, now time.Time) string {
	switch c.State {
	case api.CertExpired:
		return fmt.Sprintf("certificate %s has expired", c.Name)
	case api.CertFailed:
		return fmt.Sprintf("certificate %s failed: %s", c.Name, render.OneLine(c.Error))
	}
	msg := fmt.Sprintf("certificate %s: renewal failing", c.Name)
	if c.FailingForSeconds != nil {
		msg += " for " + render.Span(*c.FailingForSeconds)
	}
	if c.Expires != nil {
		msg += ", expires in " + render.Span(*seconds(c.Expires.Sub(now)))
	}
	if c.Error != "" {
		msg += ": " + render.OneLine(c.Error)
	}
	return msg
}

// waitingLine is "newbox declares photos.example.com, which nas holds".
func waitingLine(r api.RouteRow) string {
	head := fmt.Sprintf("%s declares %s", r.Outpost, r.Hostname)
	switch {
	case r.Waiting == api.WaitingOutsideEveryZone:
		return head + ", which is outside every Zone"
	case r.Waiting == api.WaitingNotAppliedYet:
		return head + ", which is not applied yet"
	}
	if holder, ok := strings.CutPrefix(r.Waiting, "taken by "); ok {
		return head + ", which " + holder + " holds"
	}
	return head + ", which waits: " + r.Waiting
}
