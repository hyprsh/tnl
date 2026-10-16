package api

// PathZoneRm removes a Zone: POST with a ZoneRmRequest, local only.
const PathZoneRm = "/v1/zones/rm"

// ZoneRmRequest names the Zone to remove. WithRoutes removes the Routes that
// no other Zone contains; without it the removal is refused while there are any.
type ZoneRmRequest struct {
	Name       string `json:"name"`
	WithRoutes bool   `json:"with_routes,omitempty"`
}

// ZoneRmReply answers `zone rm`: the Zone, the Hostnames of the Routes removed
// with it, and those that moved to a parent Zone.
type ZoneRmReply struct {
	Name    string   `json:"name"`
	Removed []string `json:"removed"`
	Moved   []string `json:"moved"`
	// Waiting names the declared Routes among Removed, which wait again.
	Waiting []string `json:"waiting,omitempty"`
	Handovers
	Warnings
}
