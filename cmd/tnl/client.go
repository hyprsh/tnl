package main

import (
	"context"
	"flag"
	"io"
	"os"
	"sort"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/render"
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

// groups are the registered groups by name.
var groups = map[string]*group{}

func newGroup(name string) *group {
	g := &group{name: name, subs: map[string]func([]string) error{}}
	groups[name] = g
	commands[name] = g.run
	return g
}

func (g *group) run(args []string) error {
	if len(args) == 0 {
		return usageErr(g.name, "usage: tnl %s <command>; commands: %v", g.name, g.names())
	}
	sub, ok := g.subs[args[0]]
	if !ok {
		return usageErr(g.name, "unknown command \"%s %s\"; commands: %v", g.name, args[0], g.names())
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
// the process to talk to. Without it, and without TNL_STATE_DIR, parse
// discovers the one live socket (see discoverStateDir).
type clientCommand struct {
	name     string
	fs       *flag.FlagSet
	stateDir string
}

func newClientCommand(name string) *clientCommand {
	c := &clientCommand{name: name, fs: flag.NewFlagSet("tnl "+name, flag.ContinueOnError)}
	c.fs.SetOutput(io.Discard) // a bad flag is reported once, with a pointer to the help
	flagSetMade(c.fs)
	c.fs.StringVar(&c.stateDir, "state-dir", os.Getenv("TNL_STATE_DIR"),
		"the state directory of the process to talk to (TNL_STATE_DIR)")
	return c
}

// parse reads flags that may come before or after the positional arguments,
// as in `tnl zone add example.com --wildcard cloudflare`.
func (c *clientCommand) parse(args []string) (positional []string, err error) {
	for {
		if err := c.fs.Parse(args); err != nil {
			return nil, usageErr(c.name, "%v", err)
		}
		if c.fs.NArg() == 0 {
			c.stateDir, err = discoverStateDir(c.stateDir)
			if err != nil {
				err = discoverError{err}
			}
			return positional, err
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

// lsCommand makes a `tnl <group> ls` command that GETs path and prints the
// answer with print, or as JSON with --json: the answer as it came, which is
// the stable surface.
func lsCommand[L any](name, path string, print func(io.Writer, L) error) func([]string) error {
	return func(args []string) error {
		c := newClientCommand(name)
		asJSON := c.fs.Bool("json", false, "print JSON, the stable form, instead of a table")
		pos, err := c.parse(args)
		if err != nil {
			return err
		}
		if len(pos) > 0 {
			return usageErr(name, "%s takes no arguments, got %q", name, pos[0])
		}
		_, err = showReply(c, path, *asJSON, print)
		return err
	}
}

// showReply GETs path and prints the reply with print, or, with asJSON, as it
// came: the Edge's own bytes, indented, so a field this build doesn't know
// still reaches the script and doesn't stop the table. It returns the reply.
func showReply[R any](c *clientCommand, path string, asJSON bool, print func(io.Writer, R) error) (R, error) {
	reply, raw, err := api.GetLenient[R](context.Background(), c.client(), path)
	if err != nil {
		return reply, err
	}
	if asJSON {
		return reply, render.RawJSON(stdout, raw)
	}
	return reply, print(stdout, reply)
}

func (c *clientCommand) client() *api.Client {
	v, _ := versionInfo()
	return &api.Client{StateDir: c.stateDir, Version: v}
}

// settingName is how a message names a setting that is a flag with a variable:
// the flag when the command line gave it, else the variable.
func settingName(fs *flag.FlagSet, flagName, varName string) string {
	if flagGiven(fs, flagName) {
		return "--" + flagName
	}
	return varName
}

// flagGiven reports whether the flag was set on the command line.
func flagGiven(fs *flag.FlagSet, name string) bool {
	given := false
	fs.Visit(func(f *flag.Flag) { given = given || f.Name == name })
	return given
}
