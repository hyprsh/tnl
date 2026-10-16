package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/hyprsh/tnl/internal/logx"
	"github.com/hyprsh/tnl/internal/outpost"
)

// connect is in both builds: it links no Caddy and none of the Edge.
func init() {
	commands["connect"] = runConnect
}

type connectSettings struct {
	cfg outpost.Config
	log *logx.Config
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
	s.log, err = parseDaemon("connect", args, usageOut, func(fs *flag.FlagSet) {
		fs.StringVar(&s.cfg.Token, "token", os.Getenv("TNL_TOKEN"),
			"the join token from `tnl outpost add` (TNL_TOKEN); needed only to join")
		fs.StringVar(&s.cfg.StateDir, "state-dir", envOr("TNL_STATE_DIR", outpostStateDir()),
			"the state directory (TNL_STATE_DIR)")
	}, nil)
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
