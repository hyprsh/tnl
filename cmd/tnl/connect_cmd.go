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
	"path/filepath"
	"syscall"

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
	fs := flag.NewFlagSet("tnl connect", flag.ContinueOnError)
	fs.SetOutput(usageOut)
	fs.StringVar(&s.cfg.Token, "token", os.Getenv("TNL_TOKEN"),
		"the join token from `tnl outpost add` (TNL_TOKEN); needed only to join")
	fs.StringVar(&s.cfg.StateDir, "state-dir", envOr("TNL_STATE_DIR", outpostStateDir()),
		"the state directory (TNL_STATE_DIR)")
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
	if _, err := s.log.Logger(io.Discard, ""); err != nil {
		return s, configError{err}
	}
	return s, nil
}

func runConnect(args []string) error {
	s, err := parseConnectFlags(args, os.Stderr)
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
	err = outpost.Run(ctx, s.cfg)
	var ce *outpost.ConfigError
	if errors.As(err, &ce) {
		return configError{err}
	}
	return err
}
