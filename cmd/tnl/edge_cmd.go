//go:build !outpost

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hyprsh/tnl/internal/edge"
	"github.com/hyprsh/tnl/internal/logx"
)

// Edge-only commands register from //go:build !outpost files like this one.
func init() {
	commands["edge"] = runEdge
}

type edgeSettings struct {
	cfg edge.Config
	log *logx.Config
}

func parseEdgeFlags(args []string, usageOut io.Writer) (edgeSettings, error) {
	var s edgeSettings
	fs := flag.NewFlagSet("tnl edge", flag.ContinueOnError)
	fs.SetOutput(usageOut)
	fs.StringVar(&s.cfg.Address, "address", os.Getenv("TNL_ADDRESS"),
		"the DNS name Outposts dial (TNL_ADDRESS, required); a name is better than an IP")
	fs.StringVar(&s.cfg.StateDir, "state-dir", envOr("TNL_STATE_DIR", "/var/lib/tnl"),
		"the state directory (TNL_STATE_DIR)")
	fs.IntVar(&s.cfg.WireGuardPort, "wireguard-port", envInt("TNL_WIREGUARD_PORT", 51820),
		"the UDP port WireGuard listens on (TNL_WIREGUARD_PORT)")
	s.cfg.Web = &edge.Web{}
	fs.StringVar(&s.cfg.Web.ACMEEmail, "acme-email", os.Getenv("TNL_ACME_EMAIL"),
		"the contact email for the ACME account (TNL_ACME_EMAIL, optional)")
	fs.StringVar(&s.cfg.Web.ACMECA, "acme-ca", os.Getenv("TNL_ACME_CA"),
		"the ACME directory URL to get certificates from (TNL_ACME_CA); default Let's Encrypt production")
	s.log = logx.AddFlags(fs)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return s, err
		}
		return s, configError{err}
	}
	if fs.NArg() > 0 {
		return s, configError{fmt.Errorf("unexpected argument %q", fs.Arg(0))}
	}
	if s.cfg.Address == "" {
		return s, configError{fmt.Errorf("--address (TNL_ADDRESS) is required: the DNS name Outposts dial")}
	}
	if s.cfg.WireGuardPort < 1 || s.cfg.WireGuardPort > 65535 {
		return s, configError{fmt.Errorf("--wireguard-port (TNL_WIREGUARD_PORT) must be a port from 1 to 65535, got %d", s.cfg.WireGuardPort)}
	}
	if _, err := s.log.Logger(io.Discard, ""); err != nil {
		return s, configError{err}
	}
	return s, nil
}

func runEdge(args []string) error {
	s, err := parseEdgeFlags(args, os.Stderr)
	if err == flag.ErrHelp {
		return nil
	}
	if err != nil {
		return err
	}
	lg, err := s.log.Logger(os.Stderr, os.Getenv("JOURNAL_STREAM"))
	if err != nil {
		return configError{err}
	}
	slog.SetDefault(lg)
	s.cfg.Log = lg
	s.cfg.Version, s.cfg.Build = versionInfo()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	err = edge.Run(ctx, s.cfg)
	var logged *edge.LoggedError
	if errors.As(err, &logged) {
		return loggedError{err}
	}
	return err
}
