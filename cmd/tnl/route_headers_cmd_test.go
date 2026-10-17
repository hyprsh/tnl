//go:build !outpost

package main

import (
	"strings"
	"testing"
)

func TestRouteAddAndEditTakeNoHSTSAndHeadersAndLsShowsThem(t *testing.T) {
	runningEdge(t)
	tnl("zone", "add", "example.com")
	tnl("outpost", "add", "nas")
	if _, _, err := tnl("route", "add", "a.example.com", "http://a", "--outpost", "nas", "--no-hsts",
		"--header", "x-frame-options: DENY", "--header", "Content-Security-Policy: default-src 'self'"); err != nil {
		t.Fatal(err)
	}
	out, _, _ := tnl("route", "ls")
	if !strings.Contains(out, `no-hsts=true header="Content-Security-Policy: default-src 'self'" header="X-Frame-Options: DENY"`) {
		t.Fatalf("route ls:\n%s", out)
	}
	js, _, _ := tnl("route", "ls", "--json")
	if !strings.Contains(js, `"header=X-Frame-Options: DENY"`) {
		t.Fatalf("route ls --json:\n%s", js)
	}

	// Absent flags leave both alone.
	if _, _, err := tnl("route", "edit", "a.example.com", "--service", "http://b:8"); err != nil {
		t.Fatal(err)
	}
	out, _, _ = tnl("route", "ls")
	if !strings.Contains(out, "no-hsts=true") || !strings.Contains(out, "X-Frame-Options") {
		t.Fatalf("an edit of the Service lost options:\n%s", out)
	}
	// --header replaces the list.
	if _, _, err := tnl("route", "edit", "a.example.com", "--no-hsts=false", "--header", "Referrer-Policy: no-referrer"); err != nil {
		t.Fatal(err)
	}
	out, _, _ = tnl("route", "ls")
	if strings.Contains(out, "no-hsts") || strings.Contains(out, "X-Frame-Options") || !strings.Contains(out, "Referrer-Policy") {
		t.Fatalf("after replacing:\n%s", out)
	}
	// --header "" clears.
	if _, _, err := tnl("route", "edit", "a.example.com", "--header", ""); err != nil {
		t.Fatal(err)
	}
	out, _, _ = tnl("route", "ls")
	if strings.Contains(out, "header") {
		t.Fatalf("after clearing:\n%s", out)
	}

	for _, args := range [][]string{
		{"route", "add", "b.example.com", "http://a", "--outpost", "nas", "--header", "Strict-Transport-Security: x"},
		{"route", "add", "b.example.com", "http://a", "--outpost", "nas", "--header", "Connection: close"},
		{"route", "add", "b.example.com", "http://a", "--outpost", "nas", "--header", "no colon"},
		{"route", "add", "b.example.com", "http://a", "--outpost", "nas", "--header", "A: 1", "--header", "a: 2"},
		{"route", "edit", "a.example.com", "--header", "", "--header", "A: 1"},
	} {
		if _, code, err := tnl(args...); err == nil || code != 1 {
			t.Errorf("%v: err=%v code=%d", args, err, code)
		}
	}
}
