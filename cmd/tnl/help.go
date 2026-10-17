package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

// cmdHelp is one command's (or group's) help; see helpTable.
type cmdHelp struct {
	path     string // "route add"
	group    bool   // has subcommands
	usage    string // the spec table's first cell, without the leading "tnl "
	runsOn   string // the spec table's second cell
	summary  string
	args     []argHelp
	flags    []flagHelp // the command's own; --state-dir and the logging flags come from client and daemon
	examples []string
	notes    string
	client   bool // takes --state-dir and talks to the running process
	daemon   bool // takes the logging flags
	// firstArg says what the first positional argument completes to: outposts,
	// routes, zones, commands, or words:a b c.
	firstArg string
}

type argHelp struct{ name, desc string }

type flagHelp struct {
	name   string
	meta   string // the value, as in --ttl D; "=true|false" for a bool that takes a value
	def    string // the default, shown only when it is a fixed value
	env    string // the variable behind it
	repeat bool
	values string // what the value completes to, as firstArg does
	desc   string
}

// allFlags are the flags the command takes: its own, then the shared ones.
func (c *cmdHelp) allFlags() []flagHelp {
	fl := append([]flagHelp(nil), c.flags...)
	if c.client {
		fl = append(fl, stateDirFlag)
	}
	if c.daemon {
		fl = append(fl, logFlags...)
	}
	return fl
}

// helpFor finds the entry for a path such as "route add", or nil.
func helpFor(path string) *cmdHelp {
	for _, c := range helpTable {
		if c.path == path {
			return c
		}
	}
	return nil
}

// commandFor finds the registered function for a path, or nil.
func commandFor(path string) func([]string) error {
	top, sub, isSub := strings.Cut(path, " ")
	if !isSub {
		return commands[top]
	}
	if g, ok := groups[top]; ok {
		return g.subs[sub]
	}
	return nil
}

// flagSetMade lets a test see every flag set a command makes.
var flagSetHook func(*flag.FlagSet)

func flagSetMade(fs *flag.FlagSet) {
	if flagSetHook != nil {
		flagSetHook(fs)
	}
}

// usageError is a command used wrongly: the message, then a pointer to its help.
type usageError struct{ path, msg string }

func usageErr(path, format string, args ...any) error {
	return usageError{path, fmt.Sprintf(format, args...)}
}

func (e usageError) Error() string {
	return e.msg + "\nrun `" + strings.TrimSpace("tnl help "+e.path) + "` for details"
}

func isHelpFlag(a string) bool {
	switch a {
	case "-h", "--h", "-help", "--help":
		return true
	}
	return false
}

// resolvePath reads the command path off the front of args: "route add" from
// route add x --help. It stops at the first flag or word that isn't in the table.
func resolvePath(args []string) (path string, rest []string) {
	rest = args
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		next := strings.TrimSpace(path + " " + rest[0])
		if helpFor(next) == nil {
			break
		}
		path, rest = next, rest[1:]
	}
	return path, rest
}

// dispatch runs the command args name, as main does. Help is answered here,
// before any flag is parsed, at every level.
func dispatch(args []string) error {
	if len(args) == 0 {
		return exitError{2, usageErr("", "usage: tnl <command> [flags]")}
	}
	path, rest := resolvePath(args)
	for _, a := range rest {
		if a == "--" {
			break
		}
		if isHelpFlag(a) {
			return printHelp(stdout, path)
		}
	}
	if path == "" && isHelpFlag(args[0]) {
		return printHelp(stdout, "")
	}
	run, ok := commands[args[0]]
	if !ok {
		return exitError{2, usageErr("", "unknown command %q", args[0])}
	}
	return run(args[1:])
}

func init() {
	commands["help"] = func(args []string) error {
		path, rest := resolvePath(args)
		if len(rest) > 0 && !isHelpFlag(rest[0]) {
			return exitError{2, usageErr(path, "unknown command %q", strings.Join(append(strings.Fields(path), rest[0]), " "))}
		}
		return printHelp(stdout, path)
	}
}

func printHelp(w io.Writer, path string) error {
	switch c := helpFor(path); {
	case path == "":
		topHelp(w)
	case c.group:
		groupHelp(w, c)
	default:
		commandHelp(w, c)
	}
	return nil
}

func topHelp(w io.Writer) {
	fmt.Fprintf(w, "tnl: put a Service behind NAT on the internet through an Edge\n\nUsage:\n  tnl <command> [flags]\n\nCommands:\n")
	var rows [][2]string
	for _, c := range helpTable {
		if !strings.Contains(c.path, " ") {
			rows = append(rows, [2]string{c.path, c.summary + refusedNote(c.path)})
		}
	}
	writeRows(w, rows)
	fmt.Fprintf(w, "\nRun `tnl help <command>` for a command's arguments and flags, or `tnl help <group> <command>`.\n-h and --help work at every level.\n")
}

func groupHelp(w io.Writer, g *cmdHelp) {
	fmt.Fprintf(w, "tnl %s: %s%s\n\nUsage:\n  tnl %s\n\nCommands:\n", g.path, g.summary, refusedNote(g.path), g.usage)
	var rows [][2]string
	for _, c := range helpTable {
		if strings.HasPrefix(c.path, g.path+" ") {
			rows = append(rows, [2]string{strings.TrimPrefix(c.path, g.path+" "), c.summary + refusedNote(c.path)})
		}
	}
	writeRows(w, rows)
	fmt.Fprintf(w, "\nRun `tnl help %s <command>` for a command's arguments and flags.\n", g.path)
}

func commandHelp(w io.Writer, c *cmdHelp) {
	fmt.Fprintf(w, "tnl %s: %s\n", c.path, c.summary)
	if n := refusedNote(c.path); n != "" {
		fmt.Fprintf(w, "\nThis is the Outpost build: this command needs the full build, on the Edge host.\n")
	}
	fmt.Fprintf(w, "\nUsage:\n  tnl %s\n\nRuns on: %s\n", c.usage, c.runsOn)
	if len(c.args) > 0 {
		fmt.Fprintf(w, "\nArguments:\n")
		var rows [][2]string
		for _, a := range c.args {
			rows = append(rows, [2]string{a.name, a.desc})
		}
		writeRows(w, rows)
	}
	if fl := c.allFlags(); len(fl) > 0 {
		fmt.Fprintf(w, "\nFlags:\n")
		var rows [][2]string
		for _, f := range fl {
			rows = append(rows, [2]string{f.display(), f.describe()})
		}
		writeRows(w, rows)
	}
	if c.notes != "" {
		fmt.Fprintf(w, "\n%s\n", c.notes)
	}
	fmt.Fprintf(w, "\nExamples:\n")
	for _, e := range c.examples {
		fmt.Fprintf(w, "  %s\n", e)
	}
	fmt.Fprintf(w, "\n-h and --help show this help.\n")
}

// display is the flag as typed: --ttl D.
func (f flagHelp) display() string {
	switch {
	case f.meta == "":
		return f.dashed()
	case strings.HasPrefix(f.meta, "="):
		return f.dashed() + f.meta
	}
	return f.dashed() + " " + f.meta
}

// dashed is the flag's name as typed: -f for a one-letter flag, --ttl otherwise.
func (f flagHelp) dashed() string {
	if len(f.name) == 1 {
		return "-" + f.name
	}
	return "--" + f.name
}

func (f flagHelp) describe() string {
	s := f.desc
	if (f.def != "" || f.env != "") && !strings.HasSuffix(s, ".") && !f.repeat {
		s += "."
	}
	if f.repeat {
		s += " (repeatable)"
		if f.def != "" || f.env != "" {
			s += "."
		}
	}
	if f.def != "" {
		s += " Default: " + f.def + "."
	}
	if f.env != "" {
		s += " Variable: " + f.env + "."
	}
	return s
}

// writeRows prints two aligned columns, indented.
func writeRows(w io.Writer, rows [][2]string) {
	width := 0
	for _, r := range rows {
		width = max(width, len(r[0]))
	}
	for _, r := range rows {
		fmt.Fprintf(w, "  %-*s  %s\n", width, r[0], r[1])
	}
}

// refusedNote marks a command this build refuses, in a list.
func refusedNote(path string) string {
	if refusedHere(path) {
		return " (full build only)"
	}
	return ""
}
