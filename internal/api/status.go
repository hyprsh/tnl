package api

import "time"

// PathStatus is `tnl status`: GET answers the process's own status, local
// only. The Edge answers an EdgeStatus; an Outpost's relay answers its own
// type, tagged by Role, beside this one.
const PathStatus = "/v1/status"

// CertExpirySoon is how far ahead of its expiry a certificate needs attention.
const CertExpirySoon = 7 * 24 * time.Hour

// EdgeStatus answers GET PathStatus on an Edge. Its JSON is the stable surface
// of `tnl status --json`: frozen within a major version, changed only by
// adding fields (docs/spec/v1.md, Status). Times are RFC 3339 and durations
// whole seconds.
type EdgeStatus struct {
	Role     Role   `json:"role"` // RoleEdge
	Version  string `json:"version"`
	Build    string `json:"build"`
	Revision string `json:"revision,omitempty"`

	Started       time.Time `json:"started"`
	UptimeSeconds int64     `json:"uptime_seconds"`

	Address        string `json:"address"`
	WireGuardPort  int    `json:"wireguard_port"`
	KeyFingerprint string `json:"key_fingerprint"`
	StateDir       string `json:"state_dir"`
	LogLevel       string `json:"log_level"`
	AccessLog      bool   `json:"access_log"`

	Outposts     OutpostCounts `json:"outposts"`
	Routes       int           `json:"routes"` // Routes, not waiting lines
	Zones        int           `json:"zones"`
	Certificates CertSummary   `json:"certificates"`

	// Attention is one item for each problem; empty when nothing needs
	// attention, which is also what the exit code says.
	Attention []AttentionItem `json:"attention"`
}

// OutpostCounts is the Outposts by state.
type OutpostCounts struct {
	Online       int `json:"online"`
	Offline      int `json:"offline"`
	Incompatible int `json:"incompatible"`
	NotJoined    int `json:"not_joined"`
	// NeverConnected is left out when zero: it is an addition to the frozen shape.
	NeverConnected int `json:"never_connected,omitempty"`
}

// Count adds one Outpost in the state to the counts.
func (c *OutpostCounts) Count(s OutpostState) {
	switch s {
	case StateOnline:
		c.Online++
	case StateOffline:
		c.Offline++
	case StateIncompatible:
		c.Incompatible++
	case StateNeverConnected:
		c.NeverConnected++
	case StateNotJoined:
		c.NotJoined++
	}
}

// CertSummary counts the certificates held and says when the first expires.
type CertSummary struct {
	Count int `json:"count"`
	// SoonestExpiry is the earliest expiry among them; left out when none is held.
	SoonestExpiry *time.Time `json:"soonest_expiry,omitempty"`
}

// AttentionKind is the kind of an AttentionItem.
type AttentionKind string

// The kinds of AttentionItem on an Edge.
const (
	AttentionOutpostOffline      AttentionKind = "outpost offline"
	AttentionOutpostIncompatible AttentionKind = "outpost incompatible"
	AttentionCertificateFailing  AttentionKind = "certificate failing"
	AttentionCertificateExpiring AttentionKind = "certificate expiring"
	AttentionLineWaiting         AttentionKind = "line waiting"
)

// AttentionItem is one problem. Subject names the Outpost, the certificate's
// name or the waiting line's Hostname; Message is the line the table prints.
type AttentionItem struct {
	Kind    AttentionKind `json:"kind"`
	Subject string        `json:"subject"`
	Message string        `json:"message"`
	// Outpost, for a waiting line, is the Outpost that declares it.
	Outpost string `json:"outpost,omitempty"`
}
