package api

// Paths of `outpost rm` and `outpost rekey`: both POST, both local only.
const (
	PathOutpostRm    = "/v1/outposts/rm"
	PathOutpostRekey = "/v1/outposts/rekey"
)

// OutpostRmRequest names the Outpost to remove. WithRoutes removes the Routes
// through it too; without it the removal is refused while there are any.
type OutpostRmRequest struct {
	Name       string `json:"name"`
	WithRoutes bool   `json:"with_routes,omitempty"`
}

// OutpostRmResponse answers `outpost rm`: the Outpost and the Hostnames of the
// Routes that went with it.
type OutpostRmResponse struct {
	Name   string   `json:"name"`
	Routes []string `json:"routes"`
}

// OutpostRekeyRequest names the Outpost to rekey. TTL is the new token's
// lifetime, as in OutpostAddRequest.
type OutpostRekeyRequest struct {
	Name string `json:"name"`
	TTL  string `json:"ttl,omitempty"`
}
