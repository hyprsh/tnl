package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/hyprsh/tnl/internal/allow"
	"github.com/hyprsh/tnl/internal/declare"
	"github.com/hyprsh/tnl/internal/logx"
	"github.com/hyprsh/tnl/internal/outpost"
	"github.com/hyprsh/tnl/internal/settings"
)

// connectEnvVars are the TNL_* variables tnl connect reads, besides the
// TNL_ROUTE_* lines; any other is refused.
var connectEnvVars = append([]string{"TNL_TOKEN", "TNL_STATE_DIR", "TNL_ALLOW"}, logx.EnvVars...)

// routeFlag collects the repeated --route flag.
type routeFlag []string

func (r *routeFlag) String() string     { return strings.Join(*r, "; ") }
func (r *routeFlag) Set(v string) error { *r = append(*r, v); return nil }

// connect is in both builds: it links no Caddy and none of the Edge.
func init() {
	commands["connect"] = runConnect
}

type connectSettings struct {
	cfg outpost.Config
	log *logx.Config
	// declaration is the Routes this Outpost declares, checked at start. They
	// are also in cfg.Declaration, with their labels, for the Edge to be sent.
	declaration settings.Declaration
}

// outpostStateDir is $XDG_STATE_HOME/tnl-outpost, which is
// ~/.local/state/tnl-outpost, on Linux and macOS alike.
func outpostStateDir() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "tnl-outpost")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "/var/lib/tnl-outpost"
	}
	return filepath.Join(home, ".local", "state", "tnl-outpost")
}

func parseConnectFlags(args []string, usageOut io.Writer) (connectSettings, error) {
	var s connectSettings
	var err error
	var allowSpec string
	var routes routeFlag
	s.log, err = parseDaemon("connect", args, usageOut, func(fs *flag.FlagSet) {
		fs.StringVar(&s.cfg.Token, "token", os.Getenv("TNL_TOKEN"),
			"the join token from `tnl outpost add` (TNL_TOKEN); needed only to join")
		fs.StringVar(&s.cfg.StateDir, "state-dir", envOr("TNL_STATE_DIR", outpostStateDir()),
			"the state directory (TNL_STATE_DIR)")
		fs.StringVar(&allowSpec, "allow", os.Getenv("TNL_ALLOW"),
			"the Services the Edge may name: CIDRs and host:port, comma-separated (TNL_ALLOW); default none, so any")
		fs.Var(&routes, "route",
			"a Route to declare: `\"<hostname> <service-url> [--host-header V] [--insecure-skip-verify]\"`, repeatable; or set TNL_ROUTE_<LABEL> (not both)")
	}, func(*flag.FlagSet) (err error) {
		if err := settings.CheckEnv(os.Environ(), connectEnvVars, settings.RoutePrefix); err != nil {
			return err
		}
		var labels map[string]string
		if s.declaration, labels, err = settings.ParseRoutesLabelled(os.Environ(), routes); err != nil {
			return err
		}
		for _, r := range s.declaration {
			s.cfg.Declaration = append(s.cfg.Declaration, outpost.DeclaredRoute{
				Line:  declare.Line{Hostname: r.Hostname, Service: r.Service, HostHeader: r.HostHeader, InsecureSkipVerify: r.InsecureSkipVerify},
				Label: labels[r.Hostname],
			})
		}
		s.cfg.Allow, err = allow.Parse(allowSpec)
		return err
	})
	return s, err
}

func runConnect(args []string) error {
	var s connectSettings
	return runDaemon(args, func(args []string, out io.Writer) (*logx.Config, error) {
		var err error
		s, err = parseConnectFlags(args, out)
		return s.log, err
	}, func(ctx context.Context, lg *slog.Logger) error {
		s.cfg.Log = lg
		s.cfg.Version, s.cfg.Revision = versionInfo()
		s.cfg.Build = buildName
		err := outpost.Run(ctx, s.cfg)
		var ce *outpost.ConfigError
		if errors.As(err, &ce) {
			return configError{err}
		}
		return err
	})
}
