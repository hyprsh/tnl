package caddyx

import (
	"encoding/json"
	"strings"
	"testing"
)

func wildcardConfig(t *testing.T) (raw string, tls map[string]any) {
	t.Helper()
	c := Config{
		EdgeID: "e1", StateDir: t.TempDir(),
		ACME:      &ACME{CA: "https://ca.example/dir"},
		Wildcards: []string{"example.test", "lab.example.test"},
	}
	b, err := c.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	return string(b), cfg["apps"].(map[string]any)["tls"].(map[string]any)
}

// One policy per wildcard Zone, naming the Zone and the DNS provider module,
// ahead of the catch-all, and the Zone's names are automated.
func TestConfigHasOneTLSPolicyPerWildcardZoneAheadOfTheCatchAll(t *testing.T) {
	_, tls := wildcardConfig(t)
	policies := tls["automation"].(map[string]any)["policies"].([]any)
	if len(policies) != 3 {
		t.Fatalf("%d policies: %v", len(policies), policies)
	}
	p := policies[0].(map[string]any)
	subjects, _ := json.Marshal(p["subjects"])
	if string(subjects) != `["*.example.test","example.test"]` {
		t.Errorf("subjects %s", subjects)
	}
	if _, onDemand := p["on_demand"]; onDemand {
		t.Error("a wildcard policy must not be on-demand: certmagic would not renew it")
	}
	prov := p["issuers"].([]any)[0].(map[string]any)["challenges"].(map[string]any)["dns"].(map[string]any)["provider"].(map[string]any)
	if prov["name"] != "tnl" || prov["zone"] != "example.test" || prov["edge"] != "e1" {
		t.Errorf("provider %v", prov)
	}
	if policies[2].(map[string]any)["on_demand"] != true {
		t.Error("the catch-all is not last")
	}
	automate, _ := json.Marshal(tls["certificates"].(map[string]any)["automate"])
	if string(automate) != `["*.example.test","example.test","*.lab.example.test","lab.example.test"]` {
		t.Errorf("automate %s", automate)
	}
}

// ADR 0005: the config carries no credential, whatever the provider: no key of
// the JSON names one.
func TestConfigNamesNoCredentialFieldForWildcardZones(t *testing.T) {
	raw, _ := wildcardConfig(t)
	var cfg any
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, x := range v {
				for _, bad := range []string{"token", "api_key", "secret", "password", "credential"} {
					if strings.Contains(strings.ToLower(k), bad) {
						t.Errorf("the config has a %q key", k)
					}
				}
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(cfg)
}

func TestWithoutWildcardZonesTheConfigHasNoAutomateList(t *testing.T) {
	b, err := Config{EdgeID: "e1", StateDir: t.TempDir()}.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "automate") {
		t.Errorf("%s", b)
	}
}
