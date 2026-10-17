package main

import (
	"flag"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestHelpAtEveryLevelExitsZeroOnStdout(t *testing.T) {
	for _, args := range [][]string{
		{"help"}, {"-h"}, {"--help"},
		{"help", "route"}, {"route", "--help"}, {"route", "-h"},
		{"help", "route", "add"}, {"route", "add", "--help"}, {"route", "add", "x", "-h"},
		{"status", "-help"}, {"version", "--help"}, {"help", "help"}, {"completion", "-h"},
	} {
		out, code, err := tnl(args...)
		if err != nil || code != 0 || out == "" {
			t.Errorf("tnl %v: code %d, err %v, out %q", args, code, err, out)
		}
	}
}

func TestHelpForACommandExplainsIt(t *testing.T) {
	out, _, _ := tnl("help", "route", "add")
	for _, want := range []string{
		"tnl route add <hostname> <service-url>",
		"Runs on: both",
		"<service-url>",
		"--outpost NAME",
		"--insecure-skip-verify",
		"--state-dir DIR",
		"TNL_STATE_DIR",
		"Examples:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help route add lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, " -outpost") {
		t.Errorf("flags must show as --flag:\n%s", out)
	}
}

func TestHelpForAGroupListsItsCommands(t *testing.T) {
	out, _, _ := tnl("help", "route")
	for _, sub := range []string{"add", "edit", "ls", "rm"} {
		if !regexp.MustCompile(`(?m)^\s+` + sub + `\s+\S`).MatchString(out) {
			t.Errorf("help route lacks %q with a summary:\n%s", sub, out)
		}
	}
}

func TestHelpTopListsEveryCommand(t *testing.T) {
	out, _, _ := tnl("help")
	for name := range commands {
		if strings.HasPrefix(name, "__") {
			continue // hidden
		}
		if !strings.Contains(out, "  "+name+" ") {
			t.Errorf("tnl help lacks %q:\n%s", name, out)
		}
	}
}

func TestHelpShowsEnvironmentVariablesAndRepeatableFlags(t *testing.T) {
	out, _, _ := tnl("help", "connect")
	for _, want := range []string{"--token TOKEN", "TNL_TOKEN", "TNL_CREDENTIAL", "--route", "repeatable", "--log-level"} {
		if !strings.Contains(out, want) {
			t.Errorf("help connect lacks %q:\n%s", want, out)
		}
	}
}

func TestUnknownCommandPointsAtHelpAndExits2(t *testing.T) {
	_, code, err := tnl("nonesuch")
	if code != 2 || err == nil || !strings.Contains(err.Error(), "tnl help") {
		t.Errorf("code %d, err %v", code, err)
	}
	_, code, err = tnl("route", "nonesuch")
	if err == nil || !strings.Contains(err.Error(), "tnl help route") {
		t.Errorf("code %d, err %v", code, err)
	}
	_, code, err = tnl("help", "nonesuch")
	if err == nil || code != 2 {
		t.Errorf("help nonesuch: code %d, %v", code, err)
	}
}

func TestEveryCommandHasHelp(t *testing.T) {
	var have []string
	for name := range commands {
		if strings.HasPrefix(name, "__") {
			continue // hidden
		}
		g, isGroup := groups[name]
		if !isGroup {
			have = append(have, name)
			continue
		}
		have = append(have, name)
		for sub := range g.subs {
			have = append(have, name+" "+sub)
		}
	}
	for _, path := range have {
		c := helpFor(path)
		switch {
		case c == nil:
			t.Errorf("%q has no help entry", path)
		case c.summary == "" || c.usage == "":
			t.Errorf("%q: help lacks a summary or usage: %+v", path, c)
		case !c.group && (c.runsOn == "" || len(c.examples) == 0):
			t.Errorf("%q: help lacks runs-on or examples", path)
		}
	}
	if buildName == "outpost" {
		return // the table also describes commands this build refuses
	}
	for _, c := range helpTable {
		if commandFor(c.path) == nil {
			t.Errorf("help entry %q has no command", c.path)
		}
	}
}

// The flags a command really defines and the flags its help describes are the
// same set, and none lacks text.
func TestEveryFlagHasHelp(t *testing.T) {
	for _, k := range []string{"TNL_STATE_DIR", "TNL_KEY_FILE", "TNL_ADDRESS", "TNL_WIREGUARD_PORT", "TNL_ACME_EMAIL", "TNL_ACME_CA",
		"TNL_ACCESS_LOG", "TNL_TOKEN", "TNL_CREDENTIAL_FILE", "TNL_ALLOW", "TNL_LOG_LEVEL", "TNL_LOG_FORMAT"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	var sets []*flag.FlagSet
	flagSetHook = func(fs *flag.FlagSet) { sets = append(sets, fs) }
	defer func() { flagSetHook = nil }()
	checked := 0
	for _, c := range helpTable {
		run := commandFor(c.path)
		if c.group || run == nil || c.path == "help" || c.path == "completion" {
			continue
		}
		sets = nil
		run([]string{"--no-such-flag"})
		if len(sets) == 0 || sets[len(sets)-1].Name() != "tnl "+c.path {
			if len(c.allFlags()) == 0 {
				continue // takes no flags
			}
			if buildName == "outpost" {
				continue // refused in this build
			}
			t.Errorf("%s: no flag set was made", c.path)
			continue
		}
		checked++
		fs := sets[len(sets)-1]
		real := map[string]*flag.Flag{}
		fs.VisitAll(func(f *flag.Flag) {
			real[f.Name] = f
			if f.Usage == "" {
				t.Errorf("%s --%s has no usage text", c.path, f.Name)
			}
		})
		doc := map[string]bool{}
		for _, f := range c.allFlags() {
			doc[f.name] = true
			if f.desc == "" {
				t.Errorf("%s: help for --%s has no description", c.path, f.name)
			}
			r, ok := real[f.name]
			if !ok {
				t.Errorf("%s: help describes --%s, which the command lacks", c.path, f.name)
				continue
			}
			if f.def != "" && f.def != r.DefValue {
				t.Errorf("%s --%s: help says default %q, the flag has %q", c.path, f.name, f.def, r.DefValue)
			}
		}
		for name := range real {
			if !doc[name] {
				t.Errorf("%s: --%s has no help", c.path, name)
			}
		}
	}
	if checked < 3 {
		t.Errorf("only %d commands had their flags checked", checked)
	}
}

// The spec's CLI table and the help table list the same commands, with the same
// usage and the same hosts.
func TestSpecCLITableMatchesHelp(t *testing.T) {
	raw, err := os.ReadFile("../../docs/spec/v1.md")
	if err != nil {
		t.Fatal(err)
	}
	spec := map[string]string{}
	in := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "## 7. CLI reference") {
			in = true
		} else if in && strings.HasPrefix(line, "## ") {
			break
		}
		if !in || !strings.HasPrefix(line, "| `tnl ") {
			continue
		}
		line = strings.ReplaceAll(line, `\|`, "\x00")
		cells := strings.Split(strings.Trim(line, "| "), "|")
		if len(cells) != 2 {
			t.Fatalf("bad row %q", line)
		}
		usage := strings.Trim(strings.TrimSpace(cells[0]), "`")
		usage = strings.TrimPrefix(strings.ReplaceAll(usage, "\x00", "|"), "tnl ")
		spec[usage] = strings.TrimSpace(strings.ReplaceAll(cells[1], "\x00", "|"))
	}
	if len(spec) == 0 {
		t.Fatal("no CLI table found in the spec")
	}
	help := map[string]string{}
	for _, c := range helpTable {
		if !c.group {
			help[c.usage] = c.runsOn
		}
	}
	for u, r := range spec {
		if got, ok := help[u]; !ok || got != r {
			t.Errorf("spec row %q (%s): help has %q (found %v)", u, r, got, ok)
		}
	}
	for u := range help {
		if _, ok := spec[u]; !ok {
			t.Errorf("help entry %q is not in the spec's CLI table", u)
		}
	}
}
