package api

// PathZoneEdit changes a Zone's certificate mode or token: POST with a
// ZoneEditRequest, local only.
const PathZoneEdit = "/v1/zones/edit"

// ZoneEditRequest names the Zone and one change. Wildcard switches the Zone to
// a wildcard through that DNS provider, with Token. PerHostname switches it to
// a certificate per Hostname and deletes the token. Token alone replaces a
// wildcard Zone's token. Exactly one of the three is asked for.
type ZoneEditRequest struct {
	Name        string `json:"name"`
	Wildcard    string `json:"wildcard,omitempty"`
	PerHostname bool   `json:"per_hostname,omitempty"`
	Token       string `json:"token,omitempty"`
}

// ZoneEditReply answers `zone edit`: the Zone as it now is, and for a switch
// to wildcard the DNS zone and the names the certificate covers.
type ZoneEditReply struct {
	ZoneInfo
	WildcardInfo
	Warnings
}
