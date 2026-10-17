//go:build !outpost

package main

import (
	"strings"
	"testing"
)

// A per-Hostname Zone has no wildcard record to check, so `zone add` says each
// Hostname needs its own DNS record. The note is CLI-only, on stderr, and is
// not a reply warning.
func TestZoneAddForAPerHostnameZoneNotesThatEachHostnameNeedsADNSRecord(t *testing.T) {
	runningEdge(t)
	out, errOut, code, err := tnlBoth("zone", "add", "example.com")
	if err != nil || code != 0 {
		t.Fatalf("%q %q %v", out, errOut, err)
	}
	if !strings.Contains(errOut, "note: each Hostname in example.com needs its own DNS record, or a wildcard record, pointing at the Edge") {
		t.Errorf("stderr lacks the note: %q", errOut)
	}
	if strings.Contains(out, "note:") || strings.Contains(errOut, "warning:") {
		t.Errorf("the note is on stdout or is a warning: %q %q", out, errOut)
	}
}

func TestZoneAddForAWildcardZoneHasNoDNSRecordNote(t *testing.T) {
	edgeWildcards = new(gotToken)
	t.Cleanup(func() { edgeWildcards = nil })
	runningEdge(t)
	withStdin(t, "tok\n")
	_, errOut, _, err := tnlBoth("zone", "add", "example.com", "--wildcard", "cloudflare")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut, "note:") {
		t.Errorf("a wildcard Zone got the note: %q", errOut)
	}
}
