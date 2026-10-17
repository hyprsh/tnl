package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/hyprsh/tnl/internal/api"
)

func init() { commands["logs"] = logsCmd }

// logsCmd prints the recent log lines the running Edge or Outpost holds in its
// ring, in the format it logs in, or follows new ones. It exits 2 when no
// process is found, as `tnl status` does.
func logsCmd(args []string) error {
	c := newClientCommand("logs")
	follow := c.fs.Bool("f", false, "keep printing new lines")
	n := c.fs.Int("n", 0, "print only the last N lines")
	since := c.fs.Duration("since", 0, "print only lines newer than this")
	level := c.fs.String("level", "", "print only lines at this level or above")
	asJSON := c.fs.Bool("json", false, "print JSON lines whatever the process logs")
	pos, err := c.parse(args)
	var de discoverError
	if errors.As(err, &de) {
		return exitError{exitNotFound, err}
	}
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usageErr("logs", "logs takes no arguments, got %q", pos[0])
	}
	if *n < 0 || *since < 0 {
		return usageErr("logs", "-n and --since can't be negative")
	}
	if _, ok := api.ParseLogLevel(*level); *level != "" && !ok {
		return usageErr("logs", "--level %q: want error, warn, info or debug", *level)
	}
	if sock := filepath.Join(c.stateDir, api.SocketName); probe(sock) != socketLive {
		return exitError{exitNotFound, fmt.Errorf("can't open %s: no Edge or Outpost is running there, or this user may not open its socket (try sudo)", sock)}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	q := api.LogsQuery{Follow: *follow, N: *n, Since: *since, Level: *level, JSON: *asJSON}
	body, _, err := c.client().Download(ctx, api.PathLogs+"?"+q.Values().Encode())
	if err != nil {
		return err
	}
	defer body.Close()
	w := bufio.NewWriter(stdout)
	// Copy in chunks and flush after each, so a followed line shows at once.
	buf := make([]byte, 32<<10)
	for {
		m, rerr := body.Read(buf)
		w.Write(buf[:m])
		w.Flush()
		if rerr == io.EOF || ctx.Err() != nil {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}
