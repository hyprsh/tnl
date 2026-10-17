package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeTnl puts a `tnl` on PATH that answers `tnl __complete KIND` with canned
// names, so the scripts' live completion can be tested without a process.
func fakeTnl(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$2\" in outposts) echo office; echo nas;; routes) echo app.example.com;; zones) echo example.com;; esac\n"
	if err := os.WriteFile(filepath.Join(dir, "tnl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir + ":" + os.Getenv("PATH")
}

// complete runs the generated script in a real shell for the words typed so
// far (the last is the one being completed), and returns the candidates.
func complete(t *testing.T, shell string, words ...string) []string {
	t.Helper()
	exe, err := exec.LookPath(shell)
	if err != nil {
		t.Skipf("no %s on this machine", shell)
	}
	script, _, _ := tnl("completion", shell)
	file := filepath.Join(t.TempDir(), "completion."+shell)
	if err := os.WriteFile(file, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = strconv.Quote(w)
	}
	var run string
	switch shell {
	case "bash":
		run = `source ` + file + `; COMP_WORDS=(` + strings.Join(quoted, " ") + `); COMP_CWORD=` + strconv.Itoa(len(words)-1) +
			`; _tnl; printf '%s\n' "${COMPREPLY[@]}"`
	case "zsh":
		// No completion system here: compadd prints, and (unlike the real one)
		// does not filter by the word being typed.
		run = `source ` + file + `; words=(` + strings.Join(quoted, " ") + `); CURRENT=` + strconv.Itoa(len(words)) +
			`; compadd() { shift; printf '%s\n' "$@"; }; _files() { echo FILES; }; _tnl`
	}
	cmd := exec.Command(exe, "-c", run)
	cmd.Env = append(os.Environ(), "PATH="+fakeTnl(t))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %q: %v\n%s", shell, words, err, out)
	}
	return strings.Fields(string(out))
}

func has(got []string, want ...string) bool {
	for _, w := range want {
		if !contains(got, w) {
			return false
		}
	}
	return true
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestCompletionScriptsAreGeneratedForThreeShells(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		out, code, err := tnl("completion", shell)
		if err != nil || code != 0 || !strings.Contains(out, "route add") || !strings.Contains(out, "--insecure-skip-verify") {
			t.Errorf("%s: code %d, err %v, out %q", shell, code, err, out)
		}
	}
	_, _, err := tnl("completion", "powershell")
	if err == nil || !strings.Contains(err.Error(), "tnl help completion") {
		t.Errorf("an unknown shell: %v", err)
	}
	if _, _, err := tnl("completion"); err == nil {
		t.Error("no shell was accepted")
	}
	if fish, _, _ := tnl("completion", "fish"); !strings.Contains(fish, "complete -c tnl") {
		t.Errorf("fish script:\n%s", fish)
	}
}

func TestCompletionCompletesCommandsSubcommandsAndFlags(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			if got := complete(t, shell, "tnl", ""); !has(got, "edge", "route", "zone", "status", "help", "completion") || contains(got, "__complete") {
				t.Errorf("commands: %v", got)
			}
			if got := complete(t, shell, "tnl", "route", ""); !has(got, "add", "edit", "ls", "rm") {
				t.Errorf("subcommands: %v", got)
			}
			if got := complete(t, shell, "tnl", "route", "add", "--"); !has(got, "--outpost", "--host-header", "--insecure-skip-verify", "--state-dir", "--help") {
				t.Errorf("flags: %v", got)
			}
			if got := complete(t, shell, "tnl", "edge", "--"); !has(got, "--address", "--log-level", "--zone") {
				t.Errorf("daemon flags: %v", got)
			}
			if got := complete(t, shell, "tnl", "logs", "-"); !has(got, "-f", "-n") || contains(got, "--f") || contains(got, "--n") {
				t.Errorf("one-letter flags: %v", got)
			}
			if got := complete(t, shell, "tnl", "completion", ""); !has(got, "bash", "zsh", "fish") {
				t.Errorf("shells: %v", got)
			}
			if got := complete(t, shell, "tnl", "edge", "--log-level", ""); !has(got, "debug", "info") {
				t.Errorf("flag values: %v", got)
			}
		})
	}
	if got := complete(t, "bash", "tnl", "ro"); len(got) != 1 || got[0] != "route" {
		t.Errorf("bash filters by prefix: %v", got)
	}
}

func TestCompletionCompletesNamesFromTheRunningProcess(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			if got := complete(t, shell, "tnl", "outpost", "rm", ""); !has(got, "office", "nas") {
				t.Errorf("outposts: %v", got)
			}
			if got := complete(t, shell, "tnl", "route", "add", "x.example.com", "http://h", "--outpost", ""); !has(got, "office") {
				t.Errorf("--outpost: %v", got)
			}
			if got := complete(t, shell, "tnl", "route", "rm", ""); !has(got, "app.example.com") {
				t.Errorf("routes: %v", got)
			}
			if got := complete(t, shell, "tnl", "zone", "edit", ""); !has(got, "example.com") {
				t.Errorf("zones: %v", got)
			}
			// Only the first positional is a name.
			if got := complete(t, shell, "tnl", "route", "rm", "app.example.com", ""); contains(got, "app.example.com") {
				t.Errorf("a second argument: %v", got)
			}
		})
	}
}
