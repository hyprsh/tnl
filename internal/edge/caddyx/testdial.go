package caddyx

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"time"

	"github.com/hyprsh/tnl/internal/edge/liveness"
	"github.com/hyprsh/tnl/internal/edge/routes"
	"github.com/hyprsh/tnl/internal/wire"
)

// testDialTimeout bounds a whole test dial: the wait for the Tunnel, the
// Outpost's own 5 s dial and the TLS handshake.
const testDialTimeout = 20 * time.Second

// TestDial implements routes.TestDialer. It sends the tnl header like a
// request does, once and without the retries a request gets, so `route add`
// never waits on a Service that is down. For a verified https:// Service it
// then handshakes over the connection with the verification a request uses.
func (rt *Runtime) TestDial(ctx context.Context, r routes.Route) error {
	if rt.Liveness != nil && rt.Liveness.State(r.Outpost) != liveness.Online {
		return nil
	}
	addr, ok := rt.Outposts.Addr(r.Outpost)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, testDialTimeout)
	defer cancel()
	c, err := rt.dialOutpostOnce(ctx, addr, r.Outpost, wire.Header{Service: r.Service, Hostname: r.Hostname, RequestID: newRequestID()})
	if err != nil {
		var re *ReplyError
		switch {
		case errors.As(err, &re) && re.Reply.Status == wire.StatusDialError && re.Reply.Reason == wire.ReasonAllowlist:
			return &routes.DialFailure{Kind: routes.DialAllowlist}
		case errors.As(err, &re) && re.Reply.Status == wire.StatusDialError:
			return &routes.DialFailure{Kind: routes.DialNoAnswer, Detail: re.Reply.Reason.String()}
		case errors.Is(err, errTunnelWait):
			return &routes.DialFailure{Kind: routes.DialNoAnswer, Detail: "the Tunnel to the Outpost did not come up"}
		}
		return &routes.DialFailure{Kind: routes.DialNoAnswer, Detail: err.Error()}
	}
	defer c.Close()
	if !r.TLS || r.InsecureSkipVerify {
		return nil
	}
	tc := tls.Client(c, &tls.Config{ServerName: r.ServerName, RootCAs: rt.ServiceRoots, NextProtos: []string{"http/1.1"}})
	if err := tc.HandshakeContext(ctx); err != nil {
		var cv *tls.CertificateVerificationError
		if errors.As(err, &cv) {
			return &routes.DialFailure{Kind: routes.DialCertificate, Detail: cv.Err.Error()}
		}
		return &routes.DialFailure{Kind: routes.DialTLS, Detail: fmt.Sprint(err)}
	}
	return nil
}
