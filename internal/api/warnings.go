package api

// Warnings is how a reply that changes state reports what succeeded with a
// caveat. Every mutating reply embeds it, so the field is `warnings` (an array
// of strings, omitted when empty) on all of them. The CLI prints each one to
// stderr and still exits 0; the Edge logs each at `warn` with the actor.
type Warnings struct {
	Warnings []string `json:"warnings,omitempty"`
}

// WildcardInfo is what a reply says of a wildcard Zone's certificate: the
// provider's DNS zone the Edge found by SOA lookup, and the names the
// certificate covers. Both are only there for a wildcard Zone.
type WildcardInfo struct {
	DNSZone string   `json:"dns_zone,omitempty"`
	Covers  []string `json:"covers,omitempty"`
}

// ZoneAddReply answers POST PathZones: the Zone and any warnings.
type ZoneAddReply struct {
	ZoneInfo
	WildcardInfo
	Warnings
}

// RouteRmReply answers POST PathRouteRm: the removed Route and any warnings.
type RouteRmReply struct {
	RouteInfo
	Warnings
}

// RouteEditReply answers POST PathRouteEdit: the Route and any warnings.
type RouteEditReply struct {
	RouteInfo
	Warnings
}

// RouteAddReply answers POST PathRoutes: the Route and any warnings.
type RouteAddReply struct {
	RouteInfo
	Warnings
}
