package outpost

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/render"
)

// RoutesUnavailable is what `tnl status` says of the Outpost's Routes while it
// cannot ask the Edge.
const RoutesUnavailable = "unavailable while the Tunnel is down"

// RoutesNotRead is what `tnl status` says of the Outpost's Routes when the
// Tunnel is up but the Edge's answer could not be had or read; the cause
// follows it.
const RoutesNotRead = "could not be read from the Edge: "

// statusSource builds the Outpost's answer to `tnl status`. The relay calls it
// for each request, so it reads live state and asks the Edge for the Routes
// only when the Tunnel is up.
type statusSource struct {
	cfg     Config
	st      *State
	w       *watcher
	judge   *edgeJudge
	dec     *declarer
	edge    *api.Client
	timeout time.Duration // bounds asking the Edge for the Routes
}

func (s *statusSource) status(ctx context.Context) api.OutpostStatus {
	snap := s.w.snapshot()
	incompat, tooOld, message := s.judge.verdict()
	out := api.OutpostStatus{
		Role: api.RoleOutpost, Name: s.st.Name,
		Version: s.cfg.Version, Build: s.cfg.Build, Revision: s.cfg.Revision,
		TunnelAddress: s.st.Address.String(), StateDir: s.cfg.StateDir,
		Edge: api.OutpostEdge{
			Address: s.st.EdgeAddress, IP: snap.IP.String(), State: api.TunnelUp,
			SilentSeconds: int64(snap.Silent / time.Second), Version: snap.Version,
			Incompatible: incompat, TooOld: tooOld, Message: message,
		},
		Attention: []api.AttentionItem{},
	}
	if s.cfg.Allow != nil {
		out.Allow = s.cfg.Allow.String()
	}
	if snap.Heard {
		t := snap.LastAnswer
		out.Edge.LastAnswer = &t
	}
	if !snap.Up {
		out.Edge.State = api.TunnelDown
		out.Attention = append(out.Attention, api.AttentionItem{
			Kind: api.AttentionTunnelDown, Subject: s.st.EdgeAddress,
			Message: fmt.Sprintf("no answer from the Edge for %s", render.Span(int64(snap.Silent/time.Second))),
		})
	}
	switch {
	case incompat:
		out.Attention = append(out.Attention, api.AttentionItem{Kind: api.AttentionEdgeIncompatible, Subject: s.st.EdgeAddress, Message: message})
	case tooOld:
		out.Attention = append(out.Attention, api.AttentionItem{Kind: api.AttentionEdgeTooOld, Subject: s.st.EdgeAddress, Message: message})
	}

	var mine []api.MyRoute
	if !snap.Up {
		out.RoutesUnavailable = RoutesUnavailable
	} else {
		fctx, cancel := context.WithTimeout(ctx, s.timeout)
		// Leniently: an Edge newer than this Outpost may add fields to the reply.
		reply, _, err := api.GetLenient[api.MyRoutes](fctx, s.edge, api.PathDeclaration)
		cancel()
		var ae *api.Error
		switch {
		case errors.As(err, &ae):
			out.RoutesUnavailable = ae.Message
		case err != nil:
			out.RoutesUnavailable = RoutesNotRead + err.Error()
		default:
			mine = reply.Routes
		}
	}
	waiting := map[string]api.MyRoute{}
	if out.RoutesUnavailable == "" {
		out.Routes = []api.OutpostRoute{}
		for _, r := range mine {
			if r.Waiting {
				waiting[r.Hostname] = r
				continue
			}
			out.Routes = append(out.Routes, api.OutpostRoute{Hostname: r.Hostname, Service: r.Service})
		}
	}
	for _, l := range s.dec.lines {
		d := api.DeclaredLineStatus{Hostname: l.Hostname, Service: l.Service, Variable: s.dec.labels[l.Hostname]}
		if r, ok := waiting[l.Hostname]; ok {
			// The Edge's Reason is its log wording; the surfaces word the kind.
			// An Edge that sends no kind (older, or one this build does not
			// know) leaves only that wording.
			reason := r.WaitKind.Words(r.Holder)
			if reason == "" {
				reason = r.Reason
			}
			d.Waiting, d.Reason = true, reason
			out.Attention = append(out.Attention, api.AttentionItem{
				Kind: api.AttentionDeclaredWaiting, Subject: l.Hostname,
				Message: fmt.Sprintf("%s waits: %s", l.Hostname, reason),
			})
		}
		out.Declared = append(out.Declared, d)
	}
	return out
}
