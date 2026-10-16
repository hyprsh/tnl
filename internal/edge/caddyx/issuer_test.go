package caddyx

import (
	"encoding/json"
	"testing"
)

// Caddy fills the HTTP app's ports into the issuers of the policies in the
// config it loads, but not into policies added at runtime, so the issuer
// itself carries them: the challenge servers listen on the Edge's HTTP and
// HTTPS ports, not on 80 and 443 (which a non-root process can't bind).
func TestIssuerCarriesTheChallengePorts(t *testing.T) {
	for _, c := range []Config{
		{ACME: &ACME{CA: "https://ca.test/dir"}, HTTPPort: 8080, HTTPSPort: 8443},
		{ACME: &ACME{CA: "https://ca.test/dir"}},
	} {
		wantHTTP, wantTLS := 80, 443
		if c.HTTPPort != 0 {
			wantHTTP, wantTLS = c.HTTPPort, c.HTTPSPort
		}
		raw, err := json.Marshal(c.withDefaults().issuer())
		if err != nil {
			t.Fatal(err)
		}
		var iss struct {
			Challenges struct {
				HTTP struct {
					AlternatePort int `json:"alternate_port"`
				} `json:"http"`
				TLSALPN struct {
					AlternatePort int `json:"alternate_port"`
				} `json:"tls-alpn"`
			} `json:"challenges"`
		}
		if err := json.Unmarshal(raw, &iss); err != nil {
			t.Fatal(err)
		}
		if got := iss.Challenges.HTTP.AlternatePort; got != wantHTTP {
			t.Errorf("http challenge port %d, want %d (%s)", got, wantHTTP, raw)
		}
		if got := iss.Challenges.TLSALPN.AlternatePort; got != wantTLS {
			t.Errorf("tls-alpn challenge port %d, want %d (%s)", got, wantTLS, raw)
		}
	}
}
