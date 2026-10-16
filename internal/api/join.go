package api

import "github.com/hyprsh/tnl/internal/keys"

// PathJoin is the join exchange: POST, from an Outpost that hasn't joined yet,
// over the Tunnel. See docs/spec/v1.md, Join exchange.
const PathJoin = "/v1/join"

// DefaultTokenTTL is how long a join token lives when `outpost add` gets no --ttl.
const DefaultTokenTTL = "1h"

// JoinRequest carries the Outpost's own new public key and its version.
type JoinRequest struct {
	PublicKey keys.Key `json:"public_key"`
	Version   string   `json:"version"`
}

// JoinReply tells the Outpost its name and the Edge's version.
type JoinReply struct {
	Name        string `json:"name"`
	EdgeVersion string `json:"edge_version"`
}
