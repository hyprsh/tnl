//go:build !outpost

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"

	"github.com/hyprsh/tnl/internal/edge"
	"github.com/hyprsh/tnl/internal/logx"
	"github.com/hyprsh/tnl/internal/settings"
)

// edgeEnvVars are the TNL_* variables tnl edge reads; any other is refused.
var edgeEnvVars = append([]string{
	"TNL_ADDRESS", "TNL_WIREGUARD_PORT", "TNL_ACME_EMAIL", "TNL_ACME_CA",
	"TNL_STATE_DIR", "TNL_ACCESS_LOG",
}, logx.EnvVars...)

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
	var err error
	envPort, envPortErr := envInt("TNL_WIREGUARD_PORT", 51820)
	envAccess, envAccessErr := envBool("TNL_ACCESS_LOG")
	s.cfg.Web = &edge.Web{}
	s.log, err = parseDaemon("edge", args, usageOut, func(fs *flag.FlagSet) {
		fs.StringVar(&s.cfg.Address, "address", os.Getenv("TNL_ADDRESS"),
			"the DNS name Outposts dial (TNL_ADDRESS, required); a name is better than an IP")
		fs.StringVar(&s.cfg.StateDir, "state-dir", envOr("TNL_STATE_DIR", "/var/lib/tnl"),
			"the state directory (TNL_STATE_DIR)")
		fs.IntVar(&s.cfg.WireGuardPort, "wireguard-port", envPort,
			"the UDP port WireGuard listens on (TNL_WIREGUARD_PORT)")
		fs.StringVar(&s.cfg.Web.ACMEEmail, "acme-email", os.Getenv("TNL_ACME_EMAIL"),
			"the contact email for the ACME account (TNL_ACME_EMAIL, optional)")
		fs.StringVar(&s.cfg.Web.ACMECA, "acme-ca", os.Getenv("TNL_ACME_CA"),
			"the ACME directory URL to get certificates from (TNL_ACME_CA); default Let's Encrypt production")
		fs.BoolVar(&s.cfg.Web.AccessLog, "access-log", envAccess,
			"also log successful requests and what the Service answered (TNL_ACCESS_LOG)")
	}, func(fs *flag.FlagSet) error {
		if err := settings.CheckEnv(os.Environ(), edgeEnvVars); err != nil {
			return err
		}
		if envAccessErr != nil && !flagGiven(fs, "access-log") {
			return envAccessErr
		}
		if s.cfg.Address == "" {
			return fmt.Errorf("--address (TNL_ADDRESS) is required: the DNS name Outposts dial")
		}
		if envPortErr != nil && !flagGiven(fs, "wireguard-port") {
			return fmt.Errorf("TNL_WIREGUARD_PORT: %w", envPortErr)
		}
		if s.cfg.WireGuardPort < 1 || s.cfg.WireGuardPort > 65535 {
			return fmt.Errorf("--wireguard-port (TNL_WIREGUARD_PORT) must be a port from 1 to 65535, got %d", s.cfg.WireGuardPort)
		}
		return nil
	})
	return s, err
}

func runEdge(args []string) error {
	var s edgeSettings
	return runDaemon(args, func(args []string, out io.Writer) (*logx.Config, error) {
		var err error
		s, err = parseEdgeFlags(args, out)
		return s.log, err
	}, func(ctx context.Context, lg *slog.Logger) error {
		s.cfg.Log = lg
		s.cfg.Version, s.cfg.Revision = versionInfo()
		s.cfg.Build = buildName
		return edge.Run(ctx, s.cfg)
	})
}

// envInt reads an integer variable, def if it is unset. A value that isn't an
// integer is an error, and the value returned is def.
func envInt(name string, def int) (int, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def, fmt.Errorf("%s must be an integer, got %q", name, v)
	}
	return n, nil
}

// envBool reads a boolean variable, false if it is unset.
func envBool(name string) (bool, error) {
	v := os.Getenv(name)
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false, got %q", name, v)
	}
	return b, nil
}
