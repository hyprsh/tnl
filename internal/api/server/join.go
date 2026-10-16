package server

import (
	"context"
	"net/http"
	"net/netip"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/state"
)

// afterKey carries the functions to run once a response has been written.
type afterKey struct{}

type afterList struct{ fns []func() }

// afterReply runs fn once the response to this request is written. The join
// swaps the WireGuard peer this way: the reply travels under the one-time key
// and must be out before that key's peer goes.
func afterReply(ctx context.Context, fn func()) {
	if l, ok := ctx.Value(afterKey{}).(*afterList); ok {
		l.fns = append(l.fns, fn)
	}
}

// join is the Edge's half of the join exchange. Under the lock it checks that
// the Outpost hasn't joined and its token hasn't expired, swaps the peer's key
// and forgets the one-time key.
func (s *Server) join(ctx context.Context, actor api.Actor, req api.JoinRequest) (api.JoinReply, error) {
	name, ok := actor.OutpostName()
	if !ok {
		return api.JoinReply{}, api.Errorf(http.StatusForbidden, "%s may not join; only an Outpost that hasn't joined does", actor)
	}
	if req.PublicKey == (keys.Key{}) {
		return api.JoinReply{}, api.Errorf(http.StatusBadRequest, "the join request has no public key")
	}
	var old keys.Key
	var address netip.Addr
	err := s.cfg.Store.Update(actor, func(st *state.State) error {
		for i := range st.Outposts {
			o := &st.Outposts[i]
			if o.Name != name {
				continue
			}
			if o.OneTimeKey == nil {
				return api.Errorf(http.StatusForbidden, "Outpost %s has already joined", name)
			}
			if !time.Now().Before(o.TokenExpires) {
				return api.Errorf(http.StatusForbidden, "the join token for Outpost %s has expired; make a new one with `tnl outpost add` or `tnl outpost rekey`", name)
			}
			old, address = o.PublicKey, o.Address
			o.PublicKey, o.OneTimeKey, o.TokenExpires = req.PublicKey, nil, time.Time{}
			return nil
		}
		return api.Errorf(http.StatusForbidden, "no Outpost %s", name)
	})
	if err != nil {
		return api.JoinReply{}, err
	}
	s.cfg.Log.Info("outpost joined", "actor", actor.String(), "outpost", name, "version", req.Version)
	if s.cfg.SwapPeer != nil {
		// The new key's peer can handshake from now on; it takes the Tunnel
		// address over once the reply, which travels under the old key, is out.
		if s.cfg.PreparePeer != nil {
			if err := s.cfg.PreparePeer(req.PublicKey); err != nil {
				s.cfg.Log.Error("could not add the Outpost's new WireGuard peer", "outpost", name, "error", err)
			}
		}
		afterReply(ctx, func() { s.cfg.SwapPeer(old, req.PublicKey, address) })
	}
	return api.JoinReply{Name: name, EdgeVersion: s.cfg.Version}, nil
}
