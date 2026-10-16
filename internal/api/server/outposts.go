package server

import (
	"context"
	"net/http"
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
func (s *Server) outpostAdd(_ context.Context, actor api.Actor, req api.OutpostAddRequest) (api.OutpostAddResponse, error) {
	if !actor.Local() {
		return api.OutpostAddResponse{}, api.Errorf(http.StatusForbidden, "%s may not add Outposts; run `tnl outpost add` on the Edge host", actor)
	}
	if err := validOutpostName(req.Name); err != nil {
		return api.OutpostAddResponse{}, err
	}
	ttl, err := tokenTTL(req.TTL)
	if err != nil {
		return api.OutpostAddResponse{}, err
	}
	priv, pub, err := keys.Generate()
	if err != nil {
		return api.OutpostAddResponse{}, err
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
		return api.OutpostAddResponse{}, err
	}
	s.cfg.Log.Info("outpost added", "actor", actor.String(), "outpost", o.Name, "address", o.Address.String())
	tok := token.Token{
		Address: s.cfg.Address, Port: s.cfg.WireGuardPort,
		EdgeKey: edgeKey, OneTimeKey: priv, TunnelAddr: o.Address, EdgeVersion: s.cfg.Version,
	}
	return api.OutpostAddResponse{Name: o.Name, Address: o.Address.String(), Token: tok.Encode()}, nil
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
