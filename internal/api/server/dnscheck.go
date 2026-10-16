package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/hyprsh/tnl/internal/api"
)

// Resolver is the part of *net.Resolver the DNS checks use. Tests inject one.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

const dnsCheckTimeout = 5 * time.Second

func (s *Server) resolver() Resolver {
	if s.cfg.Resolver != nil {
		return s.cfg.Resolver
	}
	return net.DefaultResolver
}

// warn logs each warning at `warn` with the actor, exactly as the CLI prints it.
// Call it after the lock is released.
func (s *Server) warn(actor api.Actor, ws []string, attrs ...any) {
	for _, w := range ws {
		s.cfg.Log.Warn("warning", append([]any{"actor", actor.String(), "warning", w}, attrs...)...)
	}
}

// zoneDNSWarnings resolves a random name under the Zone and says so if it
// doesn't point at the Edge's address. DNS checks only warn. It must run
// after the Store's lock is released.
func (s *Server) zoneDNSWarnings(ctx context.Context, zone string) []string {
	var b [4]byte
	rand.Read(b[:])
	name := "tnl-check-" + hex.EncodeToString(b[:]) + "." + zone

	ctx, cancel := context.WithTimeout(ctx, dnsCheckTimeout)
	defer cancel()
	edge, err := s.edgeAddrs(ctx)
	if err != nil {
		return []string{fmt.Sprintf("couldn't check that names under %s point at the Edge: couldn't resolve its address %s: %v", zone, s.cfg.Address, err)}
	}
	got, err := s.resolver().LookupNetIP(ctx, "ip", name)
	if err != nil || len(got) == 0 {
		why := "no addresses"
		if err != nil {
			why = err.Error()
		}
		return []string{fmt.Sprintf("names under %s don't point at the Edge: %s doesn't resolve (%s); point *.%s at %s", zone, name, why, zone, s.cfg.Address)}
	}
	for _, g := range got {
		for _, e := range edge {
			if g.Unmap() == e.Unmap() {
				return nil
			}
		}
	}
	var ips []string
	for _, g := range got {
		ips = append(ips, g.String())
	}
	return []string{fmt.Sprintf("names under %s don't point at the Edge: %s resolves to %s, not %s; point *.%s at %s", zone, name, strings.Join(ips, ", "), s.cfg.Address, zone, s.cfg.Address)}
}

// hostnameDNSWarnings resolves a Route's Hostname and says so if it doesn't
// point at the Edge's address. DNS checks only warn. It must run after the
// Store's lock is released.
func (s *Server) hostnameDNSWarnings(ctx context.Context, host string) []string {
	ctx, cancel := context.WithTimeout(ctx, dnsCheckTimeout)
	defer cancel()
	edge, err := s.edgeAddrs(ctx)
	if err != nil {
		return []string{fmt.Sprintf("couldn't check that %s points at the Edge: couldn't resolve its address %s: %v", host, s.cfg.Address, err)}
	}
	got, err := s.resolver().LookupNetIP(ctx, "ip", host)
	if err != nil || len(got) == 0 {
		why := "no addresses"
		if err != nil {
			why = err.Error()
		}
		return []string{fmt.Sprintf("%s doesn't point at the Edge: it doesn't resolve (%s); point it at %s", host, why, s.cfg.Address)}
	}
	var ips []string
	for _, g := range got {
		for _, e := range edge {
			if g.Unmap() == e.Unmap() {
				return nil
			}
		}
		ips = append(ips, g.String())
	}
	return []string{fmt.Sprintf("%s doesn't point at the Edge: it resolves to %s, not %s; point it at %s", host, strings.Join(ips, ", "), s.cfg.Address, s.cfg.Address)}
}

// certificateWarnings waits for the Hostname's certificate, up to CertWait,
// and warns if it isn't there by then. It must run after the Store's lock is
// released.
func (s *Server) certificateWarnings(ctx context.Context, host string) []string {
	if s.cfg.Certs == nil {
		return nil
	}
	wait := s.cfg.CertWait
	if wait <= 0 {
		wait = DefaultCertWait
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	if err := s.cfg.Certs.Obtain(ctx, host); err != nil {
		return []string{fmt.Sprintf("no certificate for %s yet: %v; Caddy keeps retrying", host, err)}
	}
	return nil
}

// edgeAddrs is the Edge's --address as IPs: itself if it is one, else resolved.
func (s *Server) edgeAddrs(ctx context.Context) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(s.cfg.Address); err == nil {
		return []netip.Addr{a}, nil
	}
	return s.resolver().LookupNetIP(ctx, "ip", s.cfg.Address)
}
