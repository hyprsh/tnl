package server

import (
	"context"
	"net/http"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/state"
	"github.com/hyprsh/tnl/internal/token"
)

// outpostAdd assigns the next Tunnel address, creates the one-time key pair,
// records the public half as the Outpost's peer and returns the join token. In
// slice 1 the Outpost keeps that key; slice 3 swaps it in the join exchange.
func (s *Server) outpostAdd(_ context.Context, actor api.Actor, req api.OutpostAddRequest) (api.OutpostAddResponse, error) {
	if !actor.Local() {
		return api.OutpostAddResponse{}, api.Errorf(http.StatusForbidden, "%s may not add Outposts; run `tnl outpost add` on the Edge host", actor)
	}
	if err := validOutpostName(req.Name); err != nil {
		return api.OutpostAddResponse{}, err
	}
	priv, pub, err := keys.Generate()
	if err != nil {
		return api.OutpostAddResponse{}, err
	}
	var o state.Outpost
	var edgeKey keys.Key
	err = s.cfg.Store.Update(actor.String(), func(st *state.State) error {
		for _, have := range st.Outposts {
			if have.Name == req.Name {
				return api.Errorf(http.StatusConflict, "Outpost %s already exists", req.Name)
			}
		}
		o = state.Outpost{Name: req.Name, PublicKey: pub, Address: st.AllocAddr()}
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

// validOutpostName accepts one DNS label: 1 to 63 lower-case letters, digits
// and '-', not starting or ending with '-'.
func validOutpostName(name string) error {
	bad := func(why string) error {
		return api.Errorf(http.StatusBadRequest, "invalid Outpost name %q: %s", name, why)
	}
	if name == "" {
		return api.Errorf(http.StatusBadRequest, "the Outpost needs a name")
	}
	if len(name) > 63 {
		return bad("longer than 63 characters")
	}
	if name[0] == '-' || name[len(name)-1] == '-' {
		return bad("must not start or end with '-'")
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return bad("use lower-case letters, digits and '-' only, as in a DNS label")
		}
	}
	return nil
}
