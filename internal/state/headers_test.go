package state

import (
	"reflect"
	"strings"
	"testing"
)

func TestCanonHeadersCanonicalisesSortsAndRefuses(t *testing.T) {
	got, err := CanonHeaders([]string{"x-frame-options:DENY", "Content-Security-Policy:   default-src 'self'; img-src data: "})
	if err != nil || !reflect.DeepEqual(got, []string{"Content-Security-Policy: default-src 'self'; img-src data:", "X-Frame-Options: DENY"}) {
		t.Fatalf("%q %v", got, err)
	}
	if err := CheckHeaders(got); err != nil {
		t.Fatal(err)
	}
	if CheckHeaders([]string{"x-a: b"}) == nil || CheckHeaders([]string{"B: 1", "A: 2"}) == nil {
		t.Error("a list that is not canonical was accepted")
	}
	for _, bad := range []string{
		"", "nocolon", ": v", "A:", "Bad Name: v", ":authority: v", "A: b\r\nC: d", "A: caf\u00e9",
		"Strict-Transport-Security: max-age=1", "strict-transport-security: x", "Content-Length: 1", "Content-Type: x",
		"Transfer-Encoding: chunked", "Connection: close", "Upgrade: x", "TE: trailers", "Via: x", "Server: x",
	} {
		if _, err := CanonHeaders([]string{bad}); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if _, err := CanonHeaders([]string{"A: 1", "a: 2"}); err == nil {
		t.Error("a name twice was accepted")
	}
}

func TestCanonHeadersSortsByNameNotByWholeLine(t *testing.T) {
	got, err := CanonHeaders([]string{"X-A-B: 2", "x-a: 1"})
	if err != nil || !reflect.DeepEqual(got, []string{"X-A: 1", "X-A-B: 2"}) {
		t.Fatalf("%q %v", got, err)
	}
}

func TestParseHeaderNamesAPseudoHeaderAsOne(t *testing.T) {
	_, _, err := parseHeader(":authority: x")
	if err == nil || !strings.Contains(err.Error(), "pseudo-header") {
		t.Fatalf("%v", err)
	}
}

func TestOptionsListEveryOptionWithHeadersWhole(t *testing.T) {
	r := Route{HostHeader: "h", InsecureSkipVerify: true, NoHSTS: true, Headers: []string{"A: b c"}}
	want := []string{"host-header=h", "insecure-skip-verify=true", "no-hsts=true", "header=A: b c"}
	if got := r.Options(); !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
}

func TestRouteOptionsSurviveTheFileAndAHandEditedBadHeaderIsRefused(t *testing.T) {
	st := declState(t)
	st.Routes[0].NoHSTS = true
	st.Routes[0].Headers = []string{"X-A: b c"}
	raw, err := encode(st)
	if err != nil {
		t.Fatal(err)
	}
	back, err := decode(raw)
	if err != nil || !back.Routes[0].NoHSTS || !reflect.DeepEqual(back.Routes[0].Headers, []string{"X-A: b c"}) {
		t.Fatalf("%+v %v", back, err)
	}
	st.Routes[0].Headers = []string{"x-a: b"}
	bad, _ := encode(st)
	if _, err := decode(bad); err == nil {
		t.Fatal("a non-canonical header was loaded")
	}
}
