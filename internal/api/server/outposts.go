package server

import (
	"context"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/dnsname"
	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/state"
	"github.com/hyprsh/tnl/internal/token"
)

// outpostAdd assigns the next Tunnel address, creates the one-time key pair,
// records the public half as the Outpost's peer, with the token's expiry, and
// returns the join token. The Outpost swaps that key in the join exchange.
func (s *Server) outpostAdd(_ context.Context, actor api.Actor, req api.OutpostAddRequest) (api.OutpostAddReply, error) {
	if err := localOnly(actor, "add Outposts", "outpost add"); err != nil {
		return api.OutpostAddReply{}, err
	}
	if err := validOutpostName(req.Name); err != nil {
		return api.OutpostAddReply{}, err
	}
	ttl, err := tokenTTL(req.TTL)
	if err != nil {
		return api.OutpostAddReply{}, err
	}
	priv, pub, err := keys.Generate()
	if err != nil {
		return api.OutpostAddReply{}, err
	}
	var o state.Outpost
	var edgeKey keys.Key
	err = s.cfg.Store.Update(actor, func(st *state.State) error {
		for _, have := range st.Outposts {
			if have.Name == req.Name {
				return api.Errorf(http.StatusConflict, "Outpost %s already exists", req.Name)
			}
		}
		o = state.Outpost{Name: req.Name, PublicKey: pub, Address: st.AllocAddr(), OneTimeKey: &pub, TokenExpires: time.Now().Add(ttl).Truncate(time.Millisecond)}
		st.Outposts = append(st.Outposts, o)
		edgeKey = st.PrivateKey.Public()
		return nil
	})
	if err != nil {
		return api.OutpostAddReply{}, err
	}
	s.cfg.Log.Info("outpost added", "actor", actor.String(), "outpost", o.Name, "address", o.Address.String())
	return s.joinToken(o, edgeKey, priv), nil
}

// joinToken is the reply that carries an Outpost's one-time private key.
func (s *Server) joinToken(o state.Outpost, edgeKey, oneTimePriv keys.Key) api.OutpostAddReply {
	tok := token.Token{
		Address: s.cfg.Address, Port: s.cfg.WireGuardPort,
		EdgeKey: edgeKey, OneTimeKey: oneTimePriv, TunnelAddr: o.Address, EdgeVersion: s.cfg.Version,
	}
	return api.OutpostAddReply{Name: o.Name, Address: o.Address.String(), Token: tok.Encode()}
}

// outpostRm removes the Outpost, and with WithRoutes the Routes through it. It
// then drops the peer and closes the Outpost's connections, after the store's
// lock is released. An Outpost that hasn't joined loses its token with its
// record: the token's key is no longer a peer.
func (s *Server) outpostRm(_ context.Context, actor api.Actor, req api.OutpostRmRequest) (api.OutpostRmReply, error) {
	if err := localOnly(actor, "remove Outposts", "outpost rm"); err != nil {
		return api.OutpostRmReply{}, err
	}
	var gone state.Outpost
	var hosts []string
	handed, err := s.cfg.Store.UpdateReport(actor, func(st *state.State) error {
		i := slices.IndexFunc(st.Outposts, func(o state.Outpost) bool { return o.Name == req.Name })
		if i < 0 {
			return api.Errorf(http.StatusNotFound, "no Outpost %s", req.Name)
		}
		if via := st.RoutesVia(req.Name); len(via) > 0 && !req.WithRoutes {
			return api.Errorf(http.StatusConflict, "Routes still point at Outpost %s: %s; remove them first, or pass --with-routes", req.Name, strings.Join(via, ", "))
		}
		hosts = st.RemoveRoutesVia(req.Name)
		st.RemoveDeclaration(req.Name)
		gone = st.Outposts[i]
		st.Outposts = slices.Delete(slices.Clone(st.Outposts), i, i+1)
		return nil
	})
	if err != nil {
		return api.OutpostRmReply{}, err
	}
	s.cfg.Log.Info("outpost removed", "actor", actor.String(), "outpost", gone.Name, "routes", len(hosts))
	if s.cfg.DropPeer != nil {
		s.cfg.DropPeer(gone.PublicKey, gone.Address)
	}
	if s.cfg.Conns != nil {
		s.cfg.Conns.CloseOutpost(gone.Name)
	}
	if hosts == nil {
		hosts = []string{}
	}
	return api.OutpostRmReply{Name: gone.Name, Routes: hosts, Handovers: served(handed)}, nil
}

// outpostRekey revokes the Outpost's current key at once and returns a new
// token. The new one-time key becomes the peer at the same Tunnel address, so
// the old key's Tunnel stops, and the name, address and Routes stay.
func (s *Server) outpostRekey(_ context.Context, actor api.Actor, req api.OutpostRekeyRequest) (api.OutpostAddReply, error) {
	if err := localOnly(actor, "rekey Outposts", "outpost rekey"); err != nil {
		return api.OutpostAddReply{}, err
	}
	ttl, err := tokenTTL(req.TTL)
	if err != nil {
		return api.OutpostAddReply{}, err
	}
	priv, pub, err := keys.Generate()
	if err != nil {
		return api.OutpostAddReply{}, err
	}
	var o state.Outpost
	var old, edgeKey keys.Key
	err = s.cfg.Store.Update(actor, func(st *state.State) error {
		i := slices.IndexFunc(st.Outposts, func(o state.Outpost) bool { return o.Name == req.Name })
		if i < 0 {
			return api.Errorf(http.StatusNotFound, "no Outpost %s", req.Name)
		}
		p := &st.Outposts[i]
		old = p.PublicKey
		p.PublicKey, p.OneTimeKey, p.TokenExpires = pub, &pub, time.Now().Add(ttl).Truncate(time.Millisecond)
		p.Endpoint = netip.AddrPort{}
		o, edgeKey = *p, st.PrivateKey.Public()
		return nil
	})
	if err != nil {
		return api.OutpostAddReply{}, err
	}
	s.cfg.Log.Info("outpost rekeyed", "actor", actor.String(), "outpost", o.Name, "address", o.Address.String())
	if s.cfg.RevokePeer != nil {
		s.cfg.RevokePeer(old, pub, o.Address)
	}
	if s.cfg.Conns != nil {
		s.cfg.Conns.CloseOutpost(o.Name)
	}
	return s.joinToken(o, edgeKey, priv), nil
}

// validOutpostName accepts one DNS label.
func validOutpostName(name string) error {
	if name == "" {
		return api.Errorf(http.StatusBadRequest, "the Outpost needs a name")
	}
	if err := dnsname.CheckLabel(name); err != nil {
		return api.Errorf(http.StatusBadRequest, "invalid Outpost name %q: %v", name, err)
	}
	return nil
}

// tokenTTL parses --ttl: empty is the default, and it must be positive.
func tokenTTL(s string) (time.Duration, error) {
	if s == "" {
		s = api.DefaultTokenTTL
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, api.Errorf(http.StatusBadRequest, "invalid --ttl %q: use a positive duration such as 30m or 2h", s)
	}
	return d, nil
}
