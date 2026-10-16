package api

import (
	"bytes"
	"encoding/json"
	"sort"
	"time"
)

// The `ls` replies. Their JSON is the stable surface of `tnl ... ls --json`:
// the CLI prints these types as they came, so what is here is what a script
// sees. They are frozen within a major version and change only by adding
// fields (docs/spec/v1.md, Status). Golden tests in this package pin them.
//
// Times are RFC 3339; durations are whole seconds in fields ending _seconds.
// The tables are rendered from the same values (internal/render), so a table
// never needs a clock of its own.

// The states of an Outpost, in the words every surface uses.
const (
	StateOnline       = "online"
	StateOffline      = "offline"
	StateIncompatible = "incompatible"
	StateNotJoined    = "not joined"
)

// OutpostList answers GET PathOutposts, sorted by name.
type OutpostList struct {
	Outposts []OutpostInfo `json:"outposts"`
}

// OutpostInfo is one row of `outpost ls`.
type OutpostInfo struct {
	Name  string `json:"name"`
	State string `json:"state"` // a State* constant
	// LastSeen is when the Edge last heard from the Outpost. It is left out
	// for a `not joined` Outpost and for one not yet heard from since the
	// Edge started; LastSeenAgoSeconds goes with it.
	LastSeen           *time.Time `json:"last_seen,omitempty"`
	LastSeenAgoSeconds *int64     `json:"last_seen_ago_seconds,omitempty"`
	// TokenExpires is when the join token of a `not joined` Outpost stops
	// working; TokenExpiresInSeconds is negative once it has.
	TokenExpires          *time.Time `json:"token_expires,omitempty"`
	TokenExpiresInSeconds *int64     `json:"token_expires_in_seconds,omitempty"`
	// Endpoint is the public IP:port last heard from; empty when unknown.
	Endpoint string `json:"endpoint,omitempty"`
	// Version is the version its last ping reported; empty when unknown.
	Version string `json:"version,omitempty"`
	// TunnelAddress is the Outpost's address inside the Tunnel.
	TunnelAddress string `json:"tunnel_address"`
	// Routes counts the Routes through the Outpost. DeclaredLines is the
	// number of lines of its Declaration, waiting ones included; it is left
	// out for an Outpost that declares none.
	Routes        int  `json:"routes"`
	DeclaredLines int  `json:"declared_lines,omitempty"`
	Declared      bool `json:"declared,omitempty"` // the Edge's settings declare it
}

// RouteRow is one row of `route ls`: a Route, or a line that waits. A Route
// held by one Outpost and declared by another is two rows, the Route first.
type RouteRow struct {
	RouteInfo
	// Status is the first problem with the Route, or "ok": see StatusOK and
	// the Status* constants. A waiting row's is "waiting: <reason>".
	Status string `json:"status,omitempty"`
	// Waiting is why a declared line is not a Route; empty on a Route.
	Waiting string `json:"waiting,omitempty"`
	// Extra holds fields this build does not know, sent by a newer Edge. The
	// table shows them as name=value in a last column, and --json passes them
	// on as they came.
	Extra map[string]json.RawMessage `json:"-"`
}

// Route statuses, in the order `route ls` looks for them.
const (
	StatusOK                = "ok"
	StatusOutpostNotJoined  = "outpost not joined"
	StatusOutpostOffline    = "outpost offline"
	StatusOutpostIncompat   = "outpost incompatible"
	StatusNoCertificate     = "no certificate"
	StatusCertExpired       = "certificate expired"
	StatusRenewalFailing    = "renewal failing"
	StatusWaitingPrefix     = "waiting: "
	WaitingOutsideEveryZone = "outside every Zone"
	WaitingNotAppliedYet    = "not applied yet"
)

// WaitingTakenBy is the reason of a line whose Hostname another Outpost holds.
func WaitingTakenBy(outpost string) string { return "taken by " + outpost }

// RouteList answers GET PathRoutes, sorted by Hostname.
type RouteList struct {
	Routes []RouteRow `json:"routes"`
}

type routeRowFields RouteRow // no methods: the plain encoding

// UnmarshalJSON reads a row and keeps the fields it does not know in Extra,
// where RouteInfo's strict decoding would refuse the whole reply: an Outpost
// older than its Edge still lists the Edge's Routes.
func (r *RouteRow) UnmarshalJSON(b []byte) error {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return err
	}
	var known routeRowFields
	if err := json.Unmarshal(b, &known); err != nil {
		return err
	}
	known.Extra = nil
	for k, v := range all {
		if isRouteField[k] {
			continue
		}
		if known.Extra == nil {
			known.Extra = map[string]json.RawMessage{}
		}
		known.Extra[k] = v
	}
	*r = RouteRow(known)
	return nil
}

// isRouteField names the fields this build knows.
var isRouteField = map[string]bool{
	"hostname": true, "outpost": true, "service": true, "options": true,
	"status": true, "waiting": true,
}

// MarshalJSON writes the known fields and then Extra.
func (r RouteRow) MarshalJSON() ([]byte, error) {
	r2 := routeRowFields(r)
	r2.Extra = nil
	b, err := json.Marshal(r2)
	if err != nil || len(r.Extra) == 0 {
		return b, err
	}
	var buf bytes.Buffer
	buf.Write(bytes.TrimSuffix(b, []byte("}")))
	keys := make([]string, 0, len(r.Extra))
	for k := range r.Extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		kb, _ := json.Marshal(k)
		buf.WriteByte(',')
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(r.Extra[k])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// ZoneList answers GET PathZones, sorted by name.
type ZoneList struct {
	Zones []ZoneRow `json:"zones"`
}

// ZoneRow is one row of `zone ls`.
type ZoneRow struct {
	ZoneInfo
	Declared bool `json:"declared,omitempty"` // the Edge's settings declare it
	// Certificate is a wildcard Zone's certificate.
	Certificate *CertInfo `json:"certificate,omitempty"`
	// Certificates are a per-Hostname Zone's: one for each Route Hostname in
	// it that has a certificate of its own, sorted by name.
	Certificates []CertInfo `json:"certificates,omitempty"`
}

// The states of a certificate.
const (
	CertValid          = "valid"
	CertObtaining      = "obtaining"
	CertRenewalFailing = "renewal failing"
	CertFailed         = "failed" // failing, and never obtained
	CertExpired        = "expired"
)

// CertInfo is one certificate as the Edge's certificate manager knows it, in
// memory: a restart starts it afresh and it is filled in as Caddy loads the
// certificates it keeps.
type CertInfo struct {
	Name  string `json:"name"`
	State string `json:"state"` // a Cert* constant
	// Expires is when the certificate held stops being valid; left out when
	// none is held.
	Expires *time.Time `json:"expires,omitempty"`
	// FailingForSeconds is how long attempts have been failing; left out when
	// they are not.
	FailingForSeconds *int64 `json:"failing_for_seconds,omitempty"`
	// Error is the last error in full; the table cuts it to one line.
	Error string `json:"error,omitempty"`
}

// Failing reports whether the certificate has a problem to show: failing to
// renew or to obtain, or expired.
func (c CertInfo) Failing() bool {
	return c.State == CertRenewalFailing || c.State == CertFailed || c.State == CertExpired
}
