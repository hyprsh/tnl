//go:build !outpost

package main

import (
	"strings"
	"testing"
)

func TestLogsPrintsTheProcessesOwnLines(t *testing.T) {
	runningEdge(t)
	tnl("zone", "add", "example.com")
	out, code, err := tnl("logs")
	if err != nil || code != 0 {
		t.Fatalf("%q %v %d", out, err, code)
	}
	if !strings.Contains(out, `level=info msg="zone added" actor=local zone=example.com`) || !strings.HasPrefix(out, "time=") {
		t.Errorf("output:\n%s", out)
	}
}

func TestLogsFiltersAndPrintsJSON(t *testing.T) {
	runningEdge(t)
	tnl("zone", "add", "example.com")
	tnl("zone", "add", "b.example.com")
	out, _, err := tnl("logs", "-n", "1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) != 1 || !strings.Contains(lines[0], `"msg":"zone added"`) || !strings.Contains(lines[0], `"zone":"b.example.com"`) {
		t.Errorf("-n 1 --json:\n%s", out)
	}
	if out, _, _ := tnl("logs", "--level", "error"); out != "" {
		t.Errorf("--level error:\n%s", out)
	}
	if out, _, _ := tnl("logs", "--since", "1ns"); out != "" {
		t.Errorf("--since 1ns:\n%s", out)
	}
	if _, code, err := tnl("logs", "--level", "loud"); err == nil || code == 0 || code == 2 {
		t.Errorf("a bad level: %d %v", code, err)
	}
}

// A usage error names the flag as it was typed, not the API's field.
func TestLogsUsageErrorsNameTheFlagsAsTyped(t *testing.T) {
	runningEdge(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-n", "-1"}, `-n -1: can't be negative`},
		{[]string{"--since", "-1s"}, `--since -1s: can't be negative`},
		{[]string{"--level", "loud"}, `--level "loud": want error, warn, info or debug`},
	} {
		_, code, err := tnl(append([]string{"logs"}, tc.args...)...)
		if err == nil || !strings.Contains(err.Error(), tc.want) || code == 0 || code == 2 {
			t.Errorf("tnl logs %v: %d %v, want %q", tc.args, code, err, tc.want)
		}
	}
}
