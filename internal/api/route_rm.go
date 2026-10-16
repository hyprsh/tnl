package api

// PathRouteRm removes a Route: POST with a RouteRmRequest, answered with the
// removed Route as a RouteInfo. Recorded in docs/spec/v1.md, Tunnel API.
const PathRouteRm = "/v1/routes/rm"

// RouteRmRequest names the Route to remove by its Hostname.
type RouteRmRequest struct {
	Hostname string `json:"hostname"`
}
