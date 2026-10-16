package api

import "time"

// OutpostStatus answers GET PathStatus on an Outpost's relay, which answers it
// itself, so it works while the Tunnel is down. Its JSON is as stable as
// EdgeStatus's. A client tells the two replies apart by Role.
type OutpostStatus struct {
	Role     Role   `json:"role"` // RoleOutpost
	Name     string `json:"name"`
	Version  string `json:"version"`
	Build    string `json:"build"`
	Revision string `json:"revision,omitempty"`

	TunnelAddress string `json:"tunnel_address"`
	StateDir      string `json:"state_dir"`
	// Allow is the --allow list as given; left out when the Outpost has none
	// and so forwards to every Service.
	Allow string `json:"allow,omitempty"`

	Edge OutpostEdge `json:"edge"`

	// Routes are the Outpost's own Routes as the Edge has them (waiting lines
	// are in Declared). It is null, with RoutesUnavailable saying why, when
	// the Edge could not be asked.
	Routes            []OutpostRoute `json:"routes"`
	RoutesUnavailable string         `json:"routes_unavailable,omitempty"`

	// Declared are the lines of the Outpost's own Declaration; left out when it
	// declares none.
	Declared []DeclaredLineStatus `json:"declared,omitempty"`

	// Attention is one item for each problem; empty when nothing needs
	// attention, which is also what the exit code says.
	Attention []AttentionItem `json:"attention"`
}

// The states of OutpostEdge.
const (
	TunnelUp   = "up"
	TunnelDown = "down"
)

// OutpostEdge is the Edge as its Outpost sees it.
type OutpostEdge struct {
	Address string `json:"address"` // --address as given
	IP      string `json:"ip"`      // as last resolved
	// State is TunnelUp, or TunnelDown once the Edge has been silent too long.
	State string `json:"state"`
	// LastAnswer is when the Edge last answered; left out before the first answer.
	LastAnswer    *time.Time `json:"last_answer,omitempty"`
	SilentSeconds int64      `json:"silent_seconds"`
	Version       string     `json:"version,omitempty"`
	// Incompatible is set when the Edge says this Outpost is outside its window.
	// Message is that, or the Edge being too old, in words; left out otherwise.
	Incompatible bool   `json:"incompatible"`
	Message      string `json:"message,omitempty"`
}

// OutpostRoute is one of the Outpost's Routes.
type OutpostRoute struct {
	Hostname string `json:"hostname"`
	Service  string `json:"service"`
}

// DeclaredLineStatus is one declared line. Waiting and Reason come from the
// Edge and are left out while the Tunnel is down.
type DeclaredLineStatus struct {
	Hostname string `json:"hostname"`
	Service  string `json:"service"`
	// Variable is the TNL_ROUTE_* variable the line came from.
	Variable string `json:"variable"`
	Waiting  bool   `json:"waiting,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// More kinds of AttentionItem, for an Outpost.
const (
	AttentionTunnelDown      = "tunnel down"
	AttentionEdgeIncompat    = "edge incompatible"
	AttentionEdgeTooOld      = "edge too old"
	AttentionDeclaredWaiting = "declared line waiting"
)
