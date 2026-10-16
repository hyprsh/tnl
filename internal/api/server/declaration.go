package server

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/declare"
	"github.com/hyprsh/tnl/internal/state"
)

// declaringRefusal is what `route add`, `edit` and `rm` say for a Route of an
// Outpost that declares its Routes.
func declaringRefusal(outpost string) error {
	return api.Errorf(http.StatusConflict, "%s declares its Routes; change `TNL_ROUTE_*` on %s and restart it", outpost, outpost)
}

// checkLines refuses a Declaration the Edge could not store: a bad line, a
// Hostname twice, or a line that is not in the canonical form every Outpost
// sends (otherwise its digest would never match the stored copy).
func checkLines(lines []declare.Line) error {
	seen := map[string]bool{}
	for _, l := range lines {
		host, err := dnsName("Hostname", l.Hostname, "a Route takes one exact Hostname; wildcard Routes are not supported")
		if err != nil {
			return err
		}
		if host != l.Hostname {
			return api.Errorf(http.StatusBadRequest, "the Hostname %q is not in canonical (lower-case) form", l.Hostname)
		}
		if seen[host] {
			return api.Errorf(http.StatusBadRequest, "%s is on two lines", host)
		}
		seen[host] = true
		svc, err := state.ParseService(l.Service)
		if err != nil {
			return api.Errorf(http.StatusBadRequest, "%s: %v", host, err)
		}
		if svc.Canonical != l.Service {
			return api.Errorf(http.StatusBadRequest, "%s: the Service %q is not in canonical form (%s)", host, l.Service, svc.Canonical)
		}
		if err := state.CheckHostHeader(l.HostHeader); err != nil {
			return api.Errorf(http.StatusBadRequest, "%s: %v", host, err)
		}
	}
	return nil
}

// declare applies the calling Outpost's whole Declaration in one locked change,
// as a diff by Hostname. After the lock it test-dials the new lines and those
// whose Service changed, in parallel. Certificates for new Hostnames start in
// the background through the router's publisher, and nothing waits for them.
func (s *Server) declare(ctx context.Context, actor api.Actor, req api.DeclarationRequest) (api.DeclarationReply, error) {
	own, ok := actor.OutpostName()
	if !ok {
		return api.DeclarationReply{}, api.Errorf(http.StatusForbidden, "%s may not send a Declaration; an Outpost sends its own", actor)
	}
	if err := checkLines(req.Routes); err != nil {
		return api.DeclarationReply{}, err
	}
	var res state.DeclarationResult
	handed, err := s.cfg.Store.UpdateReport(actor, func(st *state.State) error {
		if !hasOutpost(st, own) {
			return api.Errorf(http.StatusNotFound, "no Outpost %s", own)
		}
		res = st.ApplyDeclaration(own, req.Routes, time.Now().UTC().Truncate(time.Millisecond))
		return nil
	})
	if err != nil {
		return api.DeclarationReply{}, err
	}

	log := s.cfg.Log
	for _, h := range res.Added {
		log.Info("declared line applies", "actor", actor.String(), "outpost", own, "hostname", h)
	}
	for _, h := range res.Changed {
		log.Info("declared line changed", "actor", actor.String(), "outpost", own, "hostname", h)
	}
	for _, h := range res.Removed {
		log.Info("declared line removed", "actor", actor.String(), "outpost", own, "hostname", h)
	}
	log.Info("declaration applied", "actor", actor.String(), "outpost", own, "lines", len(req.Routes),
		"added", len(res.Added), "changed", len(res.Changed), "removed", len(res.Removed), "waiting", len(res.Waiting))

	var warnings []string
	for _, h := range res.Switched {
		warnings = append(warnings, fmt.Sprintf("route %s was removed: %s declares its Routes now", h, own))
	}
	if s.cfg.Conns != nil {
		for _, h := range res.Close {
			s.cfg.Conns.CloseRoute(h)
		}
	}
	warnings = append(warnings, s.testDialAll(ctx, res.Dial)...)
	s.warn(actor, warnings, "outpost", own)

	reply := api.DeclarationReply{Lines: []api.DeclaredLine{}, Removed: res.Removed, Switched: res.Switched, Handovers: served(handed), Warnings: api.Warnings{Warnings: warnings}}
	for _, h := range res.Added {
		reply.Lines = append(reply.Lines, api.DeclaredLine{Hostname: h, Outcome: api.LineAdded})
	}
	for _, h := range res.Changed {
		reply.Lines = append(reply.Lines, api.DeclaredLine{Hostname: h, Outcome: api.LineChanged})
	}
	for _, h := range res.Unchanged {
		reply.Lines = append(reply.Lines, api.DeclaredLine{Hostname: h, Outcome: api.LineUnchanged})
	}
	for _, w := range res.Waiting {
		reply.Lines = append(reply.Lines, api.DeclaredLine{Hostname: w.Hostname, Outcome: api.LineWaiting, Reason: w.Reason})
	}
	sort.Slice(reply.Lines, func(i, j int) bool { return reply.Lines[i].Hostname < reply.Lines[j].Hostname })
	return reply, nil
}

// testDialAll test-dials the Routes in parallel and returns their warnings in
// Hostname order.
func (s *Server) testDialAll(ctx context.Context, rs []state.Route) []string {
	sort.Slice(rs, func(i, j int) bool { return rs[i].Hostname < rs[j].Hostname })
	out := make([][]string, len(rs))
	var wg sync.WaitGroup
	for i, r := range rs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = s.testDialWarnings(ctx, r)
		}()
	}
	wg.Wait()
	var all []string
	for _, ws := range out {
		all = append(all, ws...)
	}
	return all
}

// myRoutes answers an Outpost with its own Routes and waiting lines.
func (s *Server) myRoutes(_ context.Context, actor api.Actor, _ struct{}) (api.MyRoutes, error) {
	own, ok := actor.OutpostName()
	if !ok {
		return api.MyRoutes{}, api.Errorf(http.StatusForbidden, "%s has no Routes of its own to fetch; run `tnl route ls`", actor)
	}
	st := s.cfg.Store.Snapshot()
	out := api.MyRoutes{Routes: []api.MyRoute{}}
	for _, e := range st.Entries(own) {
		r := api.MyRoute{Line: e.Line, Waiting: e.Waiting}
		if e.Waiting {
			w := st.Waiting(own, e.Hostname)
			r.WaitKind, r.Reason, r.Holder = w.Kind, w.String(), w.Holder
		}
		out.Routes = append(out.Routes, r)
	}
	return out, nil
}

// served is the reply's account of the waiting lines a change turned into Routes.
func served(handed []state.Handed) api.Handovers {
	var h api.Handovers
	for _, x := range handed {
		h.Served = append(h.Served, api.Served{Outpost: x.Outpost, Hostname: x.Route.Hostname})
	}
	return h
}
