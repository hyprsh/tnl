package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/render"
)

func init() { commands["logs"] = logsCmd }

// logsFlagError words what q.Validate refused with the flags as typed. Validate
// stays the one check; this only says which flag it was.
func logsFlagError(q api.LogsQuery) string {
	switch {
	case q.N < 0:
		return fmt.Sprintf("-n %d: can't be negative", q.N)
	case q.Since < 0:
		return fmt.Sprintf("--since %s: can't be negative", q.Since)
	}
	return fmt.Sprintf("--level %q: want error, warn, info or debug", q.Level)
}

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
	pos, err := c.parseExitingNotFound(args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usageErr("logs", "logs takes no arguments, got %q", pos[0])
	}
	q := api.LogsQuery{Follow: *follow, N: *n, Since: *since, Level: *level, JSON: *asJSON}
	if err := q.Validate(); err != nil {
		return usageErr("logs", "%s", logsFlagError(q))
	}
	if err := c.requireRunning(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return render.Logs(ctx, stdout, c.client(), q)
}
