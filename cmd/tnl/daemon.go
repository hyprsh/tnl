package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hyprsh/tnl/internal/logx"
)

// parseDaemon parses the flags of a long-running command: define adds the
// command's own, the logging flags are added after them, and check (optional)
// validates the result. Every failure is a configError, except -h.
func parseDaemon(name string, args []string, usageOut io.Writer, define func(*flag.FlagSet), check func(*flag.FlagSet) error) (*logx.Config, error) {
	fs := flag.NewFlagSet("tnl "+name, flag.ContinueOnError)
	fs.SetOutput(usageOut)
	define(fs)
	log := logx.AddFlags(fs)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return log, err
		}
		return log, configError{err}
	}
	if fs.NArg() > 0 {
		return log, configError{fmt.Errorf("unexpected argument %q", fs.Arg(0))}
	}
	if check != nil {
		if err := check(fs); err != nil {
			return log, configError{err}
		}
	}
	if _, err := log.Logger(io.Discard, ""); err != nil {
		return log, configError{err}
	}
	return log, nil
}

// flagGiven reports whether the flag was set on the command line.
func flagGiven(fs *flag.FlagSet, name string) bool {
	given := false
	fs.Visit(func(f *flag.Flag) { given = given || f.Name == name })
	return given
}

// runDaemon parses args with parse, sets up the logger, and runs run until
// SIGINT or SIGTERM cancels its context.
func runDaemon(args []string, parse func([]string, io.Writer) (*logx.Config, error), run func(context.Context, *slog.Logger) error) error {
	lc, err := parse(args, os.Stderr)
	if err == flag.ErrHelp {
		return nil
	}
	if err != nil {
		return err
	}
	lg, err := lc.Logger(os.Stderr, os.Getenv("JOURNAL_STREAM"))
	if err != nil {
		return configError{err}
	}
	slog.SetDefault(lg)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return run(ctx, lg)
}
