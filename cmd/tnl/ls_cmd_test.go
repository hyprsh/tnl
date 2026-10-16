//go:build !outpost

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOutpostLsListsByNameAndShowsNotJoined(t *testing.T) {
	runningEdge(t)
	tnl("outpost", "add", "zeta")
	tnl("outpost", "add", "alpha")
	out, code, err := tnl("outpost", "ls")
	if err != nil || code != 0 {
		t.Fatalf("%q %v %d", out, err, code)
	}
	rows := strings.Split(strings.TrimSpace(out), "\n")
	if len(rows) != 3 || !strings.HasPrefix(rows[1], "alpha") || !strings.HasPrefix(rows[2], "zeta") {
		t.Fatalf("rows:\n%s", out)
	}
	for _, row := range rows[1:] {
		if !strings.Contains(row, "not joined") || !strings.Contains(row, "token expires in ") {
			t.Errorf("row %q", row)
		}
	}
}

func TestJSONOnEveryLsIsTheReplyAsItCame(t *testing.T) {
	runningEdge(t)
	tnl("zone", "add", "example.com")
	tnl("outpost", "add", "nas")
	tnl("route", "add", "app.example.com", "http://localhost:3000", "--outpost", "nas", "--host-header", "app.lan")
	for cmd, key := range map[string]string{"outpost": "outposts", "route": "routes", "zone": "zones"} {
		out, _, err := tnl(cmd, "ls", "--json")
		if err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
		var got map[string][]map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil || len(got[key]) != 1 {
			t.Fatalf("%s: %v\n%s", cmd, err, out)
		}
	}
	out, _, _ := tnl("route", "ls", "--json")
	for _, want := range []string{`"hostname": "app.example.com"`, `"status": "outpost not joined"`, `"host-header=app.lan"`} {
		if !strings.Contains(out, want) {
			t.Errorf("no %s in\n%s", want, out)
		}
	}
}
