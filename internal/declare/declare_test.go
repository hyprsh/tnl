package declare

import "testing"

func TestDigestIgnoresOrderAndDiffersOnAnyField(t *testing.T) {
	a := Line{Hostname: "a.example.com", Service: "http://localhost:1"}
	b := Line{Hostname: "b.example.com", Service: "https://nas:2", HostHeader: "x", InsecureSkipVerify: true}
	if Digest([]Line{a, b}) != Digest([]Line{b, a}) {
		t.Fatal("order changed the digest")
	}
	for name, c := range map[string]Line{
		"hostname": {Hostname: "c.example.com", Service: a.Service},
		"service":  {Hostname: a.Hostname, Service: "http://localhost:2"},
		"header":   {Hostname: a.Hostname, Service: a.Service, HostHeader: "h"},
		"insecure": {Hostname: a.Hostname, Service: a.Service, InsecureSkipVerify: true},
		"no-hsts":  {Hostname: a.Hostname, Service: a.Service, NoHSTS: true},
		"headers":  {Hostname: a.Hostname, Service: a.Service, Headers: []string{"A: b"}},
	} {
		if Digest([]Line{a, b}) == Digest([]Line{c, b}) {
			t.Errorf("a different %s kept the digest", name)
		}
	}
}

func TestAnEmptyDeclarationHasADigestOfItsOwn(t *testing.T) {
	if Digest(nil) != Digest([]Line{}) {
		t.Fatal("nil and empty differ")
	}
	if Digest(nil) == Digest([]Line{{Hostname: "a.example.com", Service: "http://x:1"}}) {
		t.Fatal("empty equals non-empty")
	}
}

func TestRoutesDigestSeesWaiting(t *testing.T) {
	l := Line{Hostname: "a.example.com", Service: "http://x:1"}
	if RoutesDigest([]Entry{{Line: l}}) == RoutesDigest([]Entry{{Line: l, Waiting: true}}) {
		t.Fatal("waiting kept the digest")
	}
	if RoutesDigest(nil) == Digest(nil) {
		t.Fatal("the two digests share a domain")
	}
}

func TestHeadersAreSeparatedInTheDigestNotJoined(t *testing.T) {
	one := Line{Hostname: "a.example.com", Service: "http://x", Headers: []string{"A: b", "C: d"}}
	two := Line{Hostname: "a.example.com", Service: "http://x", Headers: []string{"A: b\x00C: d"}}
	if Digest([]Line{one}) == Digest([]Line{two}) {
		t.Fatal("two headers and one header holding a NUL share a digest")
	}
	if Digest([]Line{one}) == RoutesDigest([]Entry{{Line: one}}) {
		t.Fatal("the two digests share a prefix")
	}
}
