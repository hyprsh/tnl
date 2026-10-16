package outpost

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/declare"
	"github.com/hyprsh/tnl/internal/wire"
)

// DeclaredRoute is one line of the Outpost's Declaration. Label names the line
// in the Outpost's log (its variable, or the --route text) and never reaches
// the Edge.
type DeclaredRoute struct {
	declare.Line
	Label string
}

// sendTimeout bounds one send of the Declaration.
const sendTimeout = 60 * time.Second

// declarer sends the Outpost's Declaration when the Edge's answer to a ping
// says its copy differs, and follows the Edge's digest of the Outpost's Routes
// and waiting lines to log the lines that start to apply or to wait.
type declarer struct {
	api    *api.Client
	log    *slog.Logger
	lines  []declare.Line
	labels map[string]string // Hostname -> label

	mu        sync.Mutex
	busy      bool   // a send or a fetch is running
	refused   string // the Edge's version when it refused the Declaration whole
	didRefuse bool
	routes    *wire.Digest // the Edge's Routes digest at the last fetch
	known     map[string]api.MyRoute
}

func newDeclarer(c *api.Client, log *slog.Logger, routes []DeclaredRoute) *declarer {
	d := &declarer{api: c, log: log, labels: map[string]string{}}
	for _, r := range routes {
		d.lines = append(d.lines, r.Line)
		d.labels[r.Hostname] = r.Label
	}
	return d
}

// digest is the digest of the Declaration the ping carries.
func (d *declarer) digest() wire.Digest { return wire.Digest(declare.Digest(d.lines)) }

// answered handles the Edge's answer to a ping.
func (d *declarer) answered(ctx context.Context, pong wire.Pong) {
	a := pong.Declaration
	if a == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.busy {
		return
	}
	switch {
	case !a.Matches:
		if d.didRefuse && d.refused == pong.Version {
			return // refused whole; again only when the Edge's version changes
		}
		d.busy = true
		go d.send(ctx, pong.Version)
	case d.routes == nil || *d.routes != a.Routes:
		d.busy = true
		go d.fetch(ctx, a.Routes)
	}
}

func (d *declarer) done() {
	d.mu.Lock()
	d.busy = false
	d.mu.Unlock()
}

func (d *declarer) send(ctx context.Context, edgeVersion string) {
	defer d.done()
	cctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	req := api.DeclarationRequest{Routes: append([]declare.Line{}, d.lines...)}
	reply, err := api.Call[api.DeclarationReply](cctx, d.api, http.MethodPost, api.PathDeclaration, req)
	if err != nil {
		var ae *api.Error
		if errors.As(err, &ae) && ae.Status < http.StatusInternalServerError {
			// The Edge refused the Declaration itself; a server error is tried again.
			d.mu.Lock()
			d.refused, d.didRefuse = edgeVersion, true
			d.mu.Unlock()
			d.log.Warn("The Edge refused the Declaration; it is sent again when the Edge's version changes", "edge_version", edgeVersion, "error", ae.Message)
			return
		}
		if ctx.Err() == nil {
			d.log.Debug("could not send the Declaration", "error", err)
		}
		return
	}
	d.mu.Lock()
	d.didRefuse = false
	d.mu.Unlock()

	var added, changed, waiting int
	for _, l := range reply.Lines {
		switch l.Outcome {
		case api.LineAdded:
			added++
			d.log.Info("Route declared", "variable", d.labels[l.Hostname], "hostname", l.Hostname)
		case api.LineChanged:
			changed++
			d.log.Info("Route changed", "variable", d.labels[l.Hostname], "hostname", l.Hostname)
		case api.LineWaiting:
			waiting++
			d.log.Warn("Route waiting", "variable", d.labels[l.Hostname], "hostname", l.Hostname, "reason", l.Reason)
		}
	}
	for _, h := range reply.Removed {
		d.log.Info("Route no longer declared", "hostname", h)
	}
	for _, w := range reply.Warnings.Warnings {
		d.log.Warn(w)
	}
	d.log.Info("Declaration sent", "declared", len(d.lines), "added", added, "changed", changed, "removed", len(reply.Removed), "waiting", waiting)

	// The baseline for the Edge's digest: what it holds now.
	d.baseline(ctx, nil)
}

// fetch follows a change of the Edge's Routes digest.
func (d *declarer) fetch(ctx context.Context, digest wire.Digest) {
	defer d.done()
	d.baseline(ctx, &digest)
}

// baseline fetches the Outpost's Routes and waiting lines, logs the lines whose
// state differs from the last fetch, and remembers them. want is the digest the
// fetch answers, nil after a send.
func (d *declarer) baseline(ctx context.Context, want *wire.Digest) {
	cctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	got, err := api.Call[api.MyRoutes](cctx, d.api, http.MethodGet, api.PathDeclaration, nil)
	if err != nil {
		if ctx.Err() == nil {
			d.log.Debug("could not fetch the Outpost's Routes", "error", err)
		}
		return
	}
	now := map[string]api.MyRoute{}
	for _, r := range got.Routes {
		now[r.Hostname] = r
	}
	d.mu.Lock()
	before := d.known
	d.known = now
	if want != nil {
		d.routes = want
	} else {
		d.routes = nil // learn the digest from the next answer
	}
	d.mu.Unlock()
	if before == nil {
		// The first look after a start (a send already logged its waiting lines).
		if want != nil {
			for _, r := range got.Routes {
				if r.Waiting {
					d.log.Warn("Route waiting", "variable", d.labels[r.Hostname], "hostname", r.Hostname, "reason", r.Reason)
				}
			}
		}
		return
	}
	hosts := make([]string, 0, len(now))
	for h := range now {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		was, had := before[h]
		r := now[h]
		switch {
		case had && was.Waiting && !r.Waiting:
			d.log.Info("Route now applies", "variable", d.labels[h], "hostname", h)
		case had && !was.Waiting && r.Waiting:
			d.log.Warn("Route started waiting", "variable", d.labels[h], "hostname", h, "reason", r.Reason)
		}
	}
}
