package server

import (
	"net/http"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/logx"
)

// logs streams the Edge's own recent log lines from its ring. It streams, so
// it is not an endpoint. The Tunnel listener mounts it too, which is why it
// refuses an Outpost: an Outpost reads its own ring, never the Edge's.
func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(actorKey{}).(api.Actor)
	if err := localOnly(actor, "read the Edge's logs", "logs"); err != nil {
		s.reply(w, nil, s.logRefusal(r, actor, err))
		return
	}
	if err := api.ServeLogs(w, r, logx.RingOf(s.cfg.Log)); err != nil {
		s.reply(w, nil, err)
	}
}
