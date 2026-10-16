package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/render"
)

func init() { commands["status"] = statusCmd }

// The exit codes of `tnl status` (spec: Status).
const (
	exitAttention = 1 // running, but something needs attention
	exitNotFound  = 2 // no running Edge or Outpost found, or its socket can't be opened
)

// exitError ends the command with a given exit code. Its message, if any, is
// printed by main; with none the command has already said all it has to.
type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e exitError) Unwrap() error { return e.err }

// statusCmd prints the status of the running Edge or Outpost and exits 0 when
// nothing needs attention, 1 when something does and 2 when no process was
// found. Exit 2 needs no process, so discovery runs before any call.
func statusCmd(args []string) error {
	c := newClientCommand("status")
	asJSON := c.fs.Bool("json", false, "print JSON, the stable form, instead of text")
	pos, err := c.parse(args)
	var de discoverError
	if errors.As(err, &de) {
		return exitError{exitNotFound, err}
	}
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return fmt.Errorf("status takes no arguments, got %q", pos[0])
	}
	if sock := filepath.Join(c.stateDir, api.SocketName); probe(sock) != socketLive {
		return exitError{exitNotFound, fmt.Errorf("can't open %s: no Edge or Outpost is running there, or this user may not open its socket (try sudo)", sock)}
	}
	ctx := context.Background()
	info, err := c.client().Info(ctx)
	if err != nil {
		return err
	}
	var attention []api.AttentionItem
	if info.Role == api.RoleEdge {
		s, err := api.Call[api.EdgeStatus](ctx, c.client(), http.MethodGet, api.PathStatus, nil)
		if err != nil {
			return err
		}
		attention = s.Attention
		err = printStatus(*asJSON, s, func() error { return render.EdgeStatus(stdout, s) })
		if err != nil {
			return err
		}
	} else {
		s, err := api.Call[api.OutpostStatus](ctx, c.client(), http.MethodGet, api.PathStatus, nil)
		if err != nil {
			return err
		}
		attention = s.Attention
		err = printStatus(*asJSON, s, func() error { return render.OutpostStatus(stdout, s, time.Now()) })
		if err != nil {
			return err
		}
	}
	if len(attention) > 0 {
		return exitError{code: exitAttention}
	}
	return nil
}

// printStatus prints the reply as JSON, or as text by text.
func printStatus(asJSON bool, reply any, text func() error) error {
	if asJSON {
		return render.JSON(stdout, reply)
	}
	return text()
}

// silent reports whether err is an exit code alone, which main does not print.
func silent(err error) bool {
	var ee exitError
	return errors.As(err, &ee) && ee.err == nil
}
