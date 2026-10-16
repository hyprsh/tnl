// Package api is the wire contract of tnl's API (HTTP/1.1 + JSON) and its
// client. The paths, the request and response types and the actor live here;
// the handlers live in api/server. This package imports neither the state
// store nor Caddy, so the Outpost build can link it.
//
// To add an endpoint: add its path and types here, its handler in api/server
// (one call to endpoint in routes), and a command in cmd/tnl that uses Call.
package api

import (
	"fmt"
	"strings"
)

const (
	// VersionHeader is on every request and every response.
	VersionHeader = "Tnl-Version"
	// SocketName is the API socket's name in the state directory.
	SocketName = "tnl.sock"
)

// Paths. They are recorded in docs/spec/v1.md, Tunnel API.
const (
	PathInfo  = "/v1/info"  // GET: role, version and build of the running process
	PathZones = "/v1/zones" // GET: list; POST: add a Zone

	PathOutposts = "/v1/outposts" // POST: add an Outpost and mint its join token
	PathRoutes   = "/v1/routes"   // GET: list; POST: add a Route
)

// Role is what a running tnl process is.
type Role string

// The roles.
const (
	RoleEdge    Role = "edge"
	RoleOutpost Role = "outpost"
)

// Actor is who a listener says the caller is. The listener sets it; it sets
// both the caller's rights and the actor= field in the logs.
type Actor string

// ActorLocal is a caller on tnl.sock: the Edge host, with full rights.
const ActorLocal Actor = "local"

// OutpostActor is a caller on the Tunnel listener (slice 3).
func OutpostActor(name string) Actor { return Actor("outpost:" + name) }

// OutpostName is the Outpost's name, if the caller is one.
func (a Actor) OutpostName() (string, bool) {
	return strings.CutPrefix(string(a), "outpost:")
}

// Local reports whether the caller has full rights.
func (a Actor) Local() bool { return a == ActorLocal }

func (a Actor) String() string { return string(a) }

// Error is a refusal or failure the server reports. Status is the HTTP status.
type Error struct {
	Status  int
	Message string
	// UnknownField, when set, is the request field the Edge refused for not
	// knowing it. It travels in the UnknownFieldHeader header, not the body.
	UnknownField string
}

// UnknownFieldHeader carries the name of the request field the Edge doesn't
// know, on the 400 that refuses it. A header, because a body field would be
// one more thing an older client refuses.
const UnknownFieldHeader = "Tnl-Unknown-Field"

// FieldNote is what the newer side knows about a request field that an older
// Edge may not: the flag or name to show, and the version that added it.
type FieldNote struct {
	Name  string // as the caller knows it, such as "--new-opt"
	Since string // the version that added the field, such as "1.5.0"
}

// FieldAdded lists, by JSON field name, the request fields added after 1.0.
// An Edge that refuses one of them says only that it doesn't know the field;
// the client composes "it needs 1.5 or newer" from this table, the one place
// that knows. v1 has none: a field added in a later version is listed here in
// the change that adds it.
var FieldAdded = map[string]FieldNote{}

func (e *Error) Error() string { return e.Message }

// Errorf makes an Error with the given HTTP status.
func Errorf(status int, format string, a ...any) *Error {
	return &Error{Status: status, Message: fmt.Sprintf(format, a...)}
}

// errorBody is how an Error travels.
type errorBody struct {
	Error string `json:"error"`
}

// Info answers PathInfo.
type Info struct {
	Role     Role   `json:"role"`
	Version  string `json:"version"`
	Build    string `json:"build"`    // "full" or "outpost"
	Revision string `json:"revision"` // the VCS revision
}

// ZoneAddRequest asks for a Zone. With a Provider it is a wildcard Zone, whose
// certificate is obtained through that DNS provider with Token. The token
// travels here once and is never shown again.
type ZoneAddRequest struct {
	Name     string `json:"name"`
	Provider string `json:"provider,omitempty"`
	Token    string `json:"token,omitempty"`
}

// ZoneInfo is a Zone as the API shows it. It never carries a token.
type ZoneInfo struct {
	Name     string `json:"name"`
	Mode     string `json:"mode"`
	Provider string `json:"provider,omitempty"`
}

// OutpostAddRequest asks for a new Outpost.
type OutpostAddRequest struct {
	Name string `json:"name"`
	// TTL is the token's lifetime as a Go duration such as "30m"; empty is
	// DefaultTokenTTL.
	TTL string `json:"ttl,omitempty"`
}

// OutpostAddReply carries the join token, which is shown once and never stored.
type OutpostAddReply struct {
	Name    string `json:"name"`
	Address string `json:"address"` // the Outpost's Tunnel address
	Token   string `json:"token"`
}

// RouteAddRequest asks for a Route. Service is a URL such as http://localhost:3000.
type RouteAddRequest struct {
	Hostname string `json:"hostname"`
	Service  string `json:"service"`
	Outpost  string `json:"outpost"`
	// HostHeader overrides the Host header sent to the Service; "" keeps the Visitor's.
	HostHeader         string `json:"host_header,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
}

// RouteInfo is a Route as the API shows it. Options holds the Route's options
// as name=value words, such as host-header=app.lan.
type RouteInfo struct {
	Hostname string   `json:"hostname"`
	Outpost  string   `json:"outpost"`
	Service  string   `json:"service"`
	Options  []string `json:"options,omitempty"`
}

// RouteList answers GET PathRoutes, sorted by Hostname.
type RouteList struct {
	Routes []RouteInfo `json:"routes"`
}

// ZoneList answers GET PathZones, sorted by name.
type ZoneList struct {
	Zones []ZoneInfo `json:"zones"`
}
