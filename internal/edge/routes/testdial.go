package routes

import "context"

// DialKind says how a test dial of a Route's Service failed.
type DialKind int

const (
	// DialAllowlist: the Outpost's allowlist refuses the Service.
	DialAllowlist DialKind = iota + 1
	// DialNoAnswer: the Service (or the Outpost) didn't answer. Detail says why.
	DialNoAnswer
	// DialCertificate: the Service's certificate fails verification.
	DialCertificate
	// DialTLS: the TLS handshake failed for another reason.
	DialTLS
)

// DialFailure is why a test dial failed, in terms the API turns into a warning.
type DialFailure struct {
	Kind   DialKind
	Detail string
}

func (f *DialFailure) Error() string { return f.Detail }

// TestDialer test-dials a Route's Service through the normal data path: the
// tnl header to the Outpost, then, for a verified https:// Service, the TLS
// handshake. It returns nil when the Service answers, a *DialFailure when it
// doesn't, and nil too when the Outpost isn't known to be online (Offline, or
// not connected yet), because the Tunnel wait would only stall `route add`.
type TestDialer interface {
	TestDial(ctx context.Context, r Route) error
}
