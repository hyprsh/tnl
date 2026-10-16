package main

import (
	"bytes"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
)

func TestTheCommandThatFreedAHostnameSaysWhoServesItNow(t *testing.T) {
	var buf bytes.Buffer
	old := stdout
	stdout = &buf
	defer func() { stdout = old }()
	printHandovers(api.Handovers{Served: []api.Served{{Outpost: "newbox", Hostname: "photos.example.com"}}})
	if got, want := buf.String(), "newbox declared photos.example.com and now serves it\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	buf.Reset()
	printHandovers(api.Handovers{})
	if buf.Len() != 0 {
		t.Fatalf("printed %q with nothing handed over", buf.String())
	}
}
