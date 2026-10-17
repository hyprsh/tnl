package api

// PathRouteEdit changes a Route: POST with a RouteEditRequest, answered with the
// Route as a RouteInfo. Recorded in docs/spec/v1.md, Tunnel API.
const PathRouteEdit = "/v1/routes/edit"

// RouteEditRequest names the Route by its Hostname and sets the fields that are
// not nil. A pointer tells "not given" from "" (clears the host header) and
// false (turns verification back on).
type RouteEditRequest struct {
	Hostname           string  `json:"hostname"`
	Service            *string `json:"service,omitempty"`
	Outpost            *string `json:"outpost,omitempty"`
	HostHeader         *string `json:"host_header,omitempty"`
	InsecureSkipVerify *bool   `json:"insecure_skip_verify,omitempty"`
	// NoHSTS false puts HSTS back on.
	NoHSTS *bool `json:"no_hsts,omitempty"`
	// Headers replaces the whole list of extra headers; an empty list clears it.
	Headers *[]string `json:"headers,omitempty"`
}
