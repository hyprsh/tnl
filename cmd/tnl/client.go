package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/hyprsh/tnl/internal/api"
)

// stdout is where commands print their result; tests swap it.
var stdout io.Writer = os.Stdout

// group is a command with subcommands, such as `tnl zone`. Each subcommand's
// file adds itself to subs in init, so tickets that add subcommands touch
// disjoint files.
type group struct {
	name string
	subs map[string]func(args []string) error
}

func newGroup(name string) *group {
	g := &group{name: name, subs: map[string]func([]string) error{}}
	commands[name] = g.run
	return g
}

func (g *group) run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tnl %s <command>; commands: %v", g.name, g.names())
	}
	sub, ok := g.subs[args[0]]
	if !ok {
		return fmt.Errorf("unknown command \"%s %s\"; commands: %v", g.name, args[0], g.names())
	}
	return sub(args[1:])
}

func (g *group) names() []string {
	var n []string
	for k := range g.subs {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// clientCommand is the flag set every API command shares: --state-dir picks
// the process to talk to. Socket discovery comes in slice 2.
type clientCommand struct {
	fs       *flag.FlagSet
	stateDir string
}

func newClientCommand(name string) *clientCommand {
	c := &clientCommand{fs: flag.NewFlagSet("tnl "+name, flag.ContinueOnError)}
	c.fs.StringVar(&c.stateDir, "state-dir", envOr("TNL_STATE_DIR", "/var/lib/tnl"),
		"the state directory of the process to talk to (TNL_STATE_DIR)")
	return c
}

// parse reads flags that may come before or after the positional arguments,
// as in `tnl zone add example.com --wildcard cloudflare`.
func (c *clientCommand) parse(args []string) (positional []string, err error) {
	for {
		if err := c.fs.Parse(args); err != nil {
			return nil, err
		}
		if c.fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, c.fs.Arg(0))
		args = c.fs.Args()[1:]
	}
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
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

// lsCommand makes a `tnl <group> ls` command that GETs path and prints a table:
// header, then the rows that rows gives for each answer.
func lsCommand[L any](name, path, header string, rows func(L) [][]string) func([]string) error {
	return func(args []string) error {
		c := newClientCommand(name)
		pos, err := c.parse(args)
		if err != nil {
			return err
		}
		if len(pos) > 0 {
			return fmt.Errorf("%s takes no arguments, got %q", name, pos[0])
		}
		list, err := api.Call[L](context.Background(), c.client(), http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, header)
		for _, row := range rows(list) {
			fmt.Fprintln(tw, strings.Join(row, "\t"))
		}
		return tw.Flush()
	}
}

// orDash is "-" for an empty table cell.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (c *clientCommand) client() *api.Client {
	v, _ := versionInfo()
	return &api.Client{StateDir: c.stateDir, Version: v}
}
