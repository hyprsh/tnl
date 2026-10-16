package server

import (
	"context"
	"fmt"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/render"
)

// outpostAttention is the kind of AttentionItem an Outpost's state is, for the
// states that are a problem: `not joined` is not.
var outpostAttention = map[api.OutpostState]api.AttentionKind{
	api.StateOffline:      api.AttentionOutpostOffline,
	api.StateIncompatible: api.AttentionOutpostIncompatible,
}

// status answers GET PathStatus: the Edge's summary and what needs attention.
// It reads the same facts the `ls` handlers do, by calling them or the
// functions they use, so the two never disagree on what an Outpost's state or
// a certificate's is.
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
		Routes: len(st.Routes), Zones: len(st.Zones), Attention: []api.AttentionItem{},
	}
	if !s.cfg.StartedAt.IsZero() {
		out.Started = s.cfg.StartedAt.UTC()
		out.UptimeSeconds = seconds(max(now.Sub(s.cfg.StartedAt), 0))
	}

	outposts, err := s.outpostList(ctx, actor, struct{}{})
	if err != nil {
		return out, err
	}
	for _, o := range outposts.Outposts {
		out.Outposts.Count(o.State)
		if kind, ok := outpostAttention[o.State]; ok {
			out.Attention = append(out.Attention, api.AttentionItem{Kind: kind, Subject: o.Name, Message: outpostMessage(o)})
		}
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
				Message: fmt.Sprintf("certificate %s expires in %s", c.Name, render.Span(seconds(c.Expires.Sub(now)))),
			})
		}
	}

	for _, w := range waitingLines(st) {
		out.Attention = append(out.Attention, api.AttentionItem{
			Kind: api.AttentionLineWaiting, Subject: w.Line.Hostname, Outpost: w.Line.Outpost,
			Message: waitingMessage(w),
		})
	}
	return out, nil
}

// outpostMessage is "Outpost nas is offline, last seen 4m ago, 3 Routes".
func outpostMessage(o api.OutpostInfo) string {
	seen := "not heard from since the Edge started"
	if o.LastSeenAgoSeconds != nil {
		seen = "last seen " + render.Span(*o.LastSeenAgoSeconds) + " ago"
	}
	return fmt.Sprintf("Outpost %s is %s, %s, %s", o.Name, o.State, seen, render.Count(o.Routes, "Route", "Routes"))
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
		msg += ", expires in " + render.Span(seconds(c.Expires.Sub(now)))
	}
	if c.Error != "" {
		msg += ": " + render.OneLine(c.Error)
	}
	return msg
}

// waitingTails say how each kind of wait ends the sentence of an Attention
// item: "newbox declares photos.example.com, which nas holds".
var waitingTails = map[api.WaitKind]func(holder string) string{
	api.WaitHeld:        func(holder string) string { return "which " + holder + " holds" },
	api.WaitOutsideZone: func(string) string { return "which is outside every Zone" },
	api.WaitNotApplied:  func(string) string { return "which is not applied yet" },
}

// waitingMessage is "newbox declares photos.example.com, which nas holds".
func waitingMessage(w declaredWait) string {
	return fmt.Sprintf("%s declares %s, %s", w.Line.Outpost, w.Line.Hostname, waitingTails[w.Why.Kind](w.Why.Holder))
}
