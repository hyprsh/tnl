// Package api is the wire contract of tnl's API (HTTP/1.1 + JSON) and its
// client. The paths, the request and response types and the actor live here;
// the handlers live in api/server. This package imports neither the state
// store nor Caddy, so the Outpost build can link it.
//
// To add an endpoint: add its path and types here, its handler in api/server
// (one call to endpoint in routes), and a command in cmd/tnl that uses Call.
package api

import "fmt"

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
)

// Actor is who a listener says the caller is. The listener sets it; it sets
// both the caller's rights and the actor= field in the logs.
type Actor string

// ActorLocal is a caller on tnl.sock: the Edge host, with full rights.
const ActorLocal Actor = "local"

// OutpostActor is a caller on the Tunnel listener (slice 3).
func OutpostActor(name string) Actor { return Actor("outpost:" + name) }

// Local reports whether the caller has full rights.
func (a Actor) Local() bool { return a == ActorLocal }

func (a Actor) String() string { return string(a) }

// Error is a refusal or failure the server reports. Status is the HTTP status.
type Error struct {
	Status  int
	Message string
}

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
	Role    string `json:"role"` // "edge" or "outpost"
	Version string `json:"version"`
	Build   string `json:"build"`
}

// ZoneAddRequest asks for a per-Hostname Zone. Wildcard options come in slice 5.
type ZoneAddRequest struct {
	Name string `json:"name"`
}

// ZoneInfo is a Zone as the API shows it. It never carries a token.
type ZoneInfo struct {
	Name     string `json:"name"`
	Mode     string `json:"mode"`
	Provider string `json:"provider,omitempty"`
}

// ZoneList answers GET PathZones, sorted by name.
type ZoneList struct {
	Zones []ZoneInfo `json:"zones"`
}
