package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/hyprsh/tnl/internal/edge/routes"
	"github.com/hyprsh/tnl/internal/state"
)

// testDialWarnings test-dials the Route's Service and says what is wrong with
// it, if anything. It runs after the lock, like every warning, and only warns:
// the Route is saved either way. Without a test dialer (no web side) it
// says nothing.
func (s *Server) testDialWarnings(ctx context.Context, r state.Route) []string {
	if s.cfg.TestDialer == nil {
		return nil
	}
	svc := r.Parsed()
	err := s.cfg.TestDialer.TestDial(ctx, routes.Route{
		Hostname: r.Hostname, Outpost: r.Outpost, Service: svc.HostPort,
		TLS: svc.TLS, ServerName: svc.Host, InsecureSkipVerify: r.InsecureSkipVerify,
	})
	var f *routes.DialFailure
	if err == nil || !errors.As(err, &f) {
		if err != nil {
			return []string{fmt.Sprintf("the test dial of %s failed: %v", r.Service, err)}
		}
		return nil
	}
	switch f.Kind {
	case routes.DialAllowlist:
		return []string{fmt.Sprintf("Outpost %s's allowlist (--allow) refuses %s: %s answers 502 until the Outpost allows it", r.Outpost, svc.HostPort, r.Hostname)}
	case routes.DialCertificate:
		return []string{fmt.Sprintf("the certificate of %s fails verification (%s): if it is self-signed or private, pass --insecure-skip-verify", r.Service, f.Detail)}
	case routes.DialTLS:
		return []string{fmt.Sprintf("the TLS handshake with %s failed (%s): is it really an https:// Service?", r.Service, f.Detail)}
	default:
		return []string{fmt.Sprintf("%s doesn't answer through Outpost %s (%s): %s answers 502 until it does", svc.HostPort, r.Outpost, f.Detail, r.Hostname)}
	}
}
