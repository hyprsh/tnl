//go:build !outpost

package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/credential"
	"github.com/hyprsh/tnl/internal/keys"
)

func writeKey(t *testing.T) (path, text string) {
	t.Helper()
	out, code, err := tnl("keygen")
	if err != nil || code != 0 {
		t.Fatalf("keygen: %v", err)
	}
	text = strings.TrimSpace(out)
	path = filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, text
}

func TestKeygenPrintsAPrivateKeyInWgGenkeyFormat(t *testing.T) {
	out, code, err := tnl("keygen")
	if err != nil || code != 0 {
		t.Fatal(err)
	}
	var k keys.Key
	if !regexp.MustCompile(`^[A-Za-z0-9+/]{43}=\n$`).MatchString(out) || k.UnmarshalText([]byte(strings.TrimSpace(out))) != nil {
		t.Fatalf("keygen printed %q", out)
	}
	if again, _, _ := tnl("keygen"); again == out {
		t.Fatal("keygen printed the same key twice")
	}
	if _, _, err := tnl("keygen", "extra"); err == nil {
		t.Fatal("keygen took an argument")
	}
}

func TestCredentialPrintsTheEdgeLineAndACredentialWithExactlyTheKeysNAPKOT(t *testing.T) {
	file, keyText := writeKey(t)
	out, code, err := tnl("credential", "nas", "--address", "edge.example.com", "--key-file", file)
	if err != nil || code != 0 {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "TNL_OUTPOST_NAS=nas ") || !strings.HasPrefix(lines[1], "TNL_CREDENTIAL=tnlc1.") {
		t.Fatalf("printed %q", out)
	}
	cred := strings.TrimPrefix(lines[1], "TNL_CREDENTIAL=")
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(cred, "tnlc1."))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 6 {
		t.Fatalf("keys %v", m)
	}
	for _, k := range []string{"n", "a", "p", "k", "o", "t"} {
		if _, ok := m[k]; !ok {
			t.Errorf("no key %q", k)
		}
	}
	// `t` is the address the Edge derives for nas, the port defaults to 51820,
	// and the Edge's line holds the public half of the credential's key.
	c, err := credential.Decode(cred)
	if err != nil {
		t.Fatal(err)
	}
	var edge keys.Key
	edge.UnmarshalText([]byte(keyText))
	pub, _ := c.PrivateKey.Public().MarshalText()
	if c.TunnelAddr != addr.Derive("nas") || c.Port != 51820 || c.Address != "edge.example.com" || c.EdgeKey != edge.Public() || lines[0] != "TNL_OUTPOST_NAS=nas "+string(pub) {
		t.Fatalf("%+v\n%s", c, lines[0])
	}
}

func TestCredentialLabelIsTheUpperCaseNameWithDashesAsUnderscoresAndTheKeyMayComeFromTNLKEY(t *testing.T) {
	_, keyText := writeKey(t)
	t.Setenv("TNL_KEY", keyText)
	t.Setenv("TNL_KEY_FILE", "")
	out, _, err := tnl("credential", "--address", "a.example.com", "my-nas", "--wireguard-port", "443")
	if err != nil || !strings.HasPrefix(out, "TNL_OUTPOST_MY_NAS=my-nas ") {
		t.Fatalf("%q %v", out, err)
	}
	c, err := credential.Decode(strings.TrimSpace(out[strings.Index(out, "TNL_CREDENTIAL=")+len("TNL_CREDENTIAL="):]))
	if err != nil || c.Port != 443 {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestCredentialRefusesWhatItCannotMintFromWithoutEchoingTheKey(t *testing.T) {
	file, keyText := writeKey(t)
	t.Setenv("TNL_KEY", "")
	t.Setenv("TNL_KEY_FILE", "")
	cases := [][]string{
		{"credential"},
		{"credential", "nas"},                   // no --address
		{"credential", "nas", "--address", "a"}, // no key
		{"credential", "NAS_1", "--address", "a", "--key-file", file},     // bad name
		{"credential", "nas", "--address", "a", "--key-file", "/no/such"}, // missing file
		{"credential", "nas", "--address", "a", "--wireguard-port", "0", "--key-file", file},
		{"credential", "a", "b", "--address", "a", "--key-file", file},
	}
	for _, args := range cases {
		out, _, err := tnl(args...)
		if err == nil || out != "" {
			t.Errorf("%v: printed %q, err %v", args, out, err)
			continue
		}
		if strings.Contains(err.Error(), keyText) {
			t.Errorf("%v: the error carries the key", args)
		}
	}
	// Both forms of the key are refused.
	t.Setenv("TNL_KEY", keyText)
	if _, _, err := tnl("credential", "nas", "--address", "a", "--key-file", file); err == nil || !strings.Contains(err.Error(), "both set") || strings.Contains(err.Error(), keyText) {
		t.Errorf("both forms: %v", err)
	}
}

func clearEdgeEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{"TNL_ADDRESS", "TNL_KEY", "TNL_KEY_FILE", "TNL_STATE_DIR", "TNL_WIREGUARD_PORT", "TNL_ACCESS_LOG"} {
		t.Setenv(v, "")
	}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "TNL_ZONE_") || strings.HasPrefix(name, "TNL_OUTPOST_") || strings.HasPrefix(name, "TNL_ROUTE_") {
			t.Setenv(name, "")
			os.Unsetenv(name)
		}
	}
	t.Setenv("TNL_ADDRESS", "edge.example.com")
}

func TestEdgeReadsItsKeyAndDeclaredLinesFromEitherForm(t *testing.T) {
	clearEdgeEnv(t)
	file, keyText := writeKey(t)
	_, pubText := writeKey(t)
	var priv, pub keys.Key
	priv.UnmarshalText([]byte(keyText))
	pub = priv.Public()
	pubB, _ := pub.MarshalText()
	_ = pubText
	t.Setenv("TNL_KEY_FILE", file)
	t.Setenv("TNL_ZONE_MAIN", "example.com")
	t.Setenv("TNL_OUTPOST_NAS", "nas "+string(pubB))
	s, err := parse(t)
	if err != nil {
		t.Fatal(err)
	}
	if s.cfg.Key == nil || *s.cfg.Key != priv || s.cfg.KeySource != "TNL_KEY_FILE" || len(s.cfg.Zones) != 1 || len(s.cfg.Outposts) != 1 {
		t.Fatalf("%+v", s.cfg)
	}
	// The same through the flags.
	t.Setenv("TNL_KEY_FILE", "")
	t.Setenv("TNL_ZONE_MAIN", "")
	os.Unsetenv("TNL_ZONE_MAIN")
	os.Unsetenv("TNL_OUTPOST_NAS")
	s, err = parse(t, "--key-file", file, "--zone", "example.com", "--outpost", "nas "+string(pubB))
	if err != nil || s.cfg.Key == nil || s.cfg.KeySource != "--key-file" || len(s.cfg.Zones) != 1 || len(s.cfg.Outposts) != 1 {
		t.Fatalf("%+v %v", s.cfg, err)
	}
}

func TestEdgeSettingsErrorsExit78NamingTheVariableAndNeverEchoingASecret(t *testing.T) {
	clearEdgeEnv(t)
	file, keyText := writeKey(t)
	const token = "tok-S3CRET-value"
	_, otherKey := writeKey(t)
	cases := []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{"bad zone line", map[string]string{"TNL_ZONE_MAIN": "example.com --wildcrd x"}, nil, "TNL_ZONE_MAIN: unknown option --wildcrd"},
		{"bad outpost line", map[string]string{"TNL_OUTPOST_NAS": "nas notakey"}, nil, "TNL_OUTPOST_NAS: the public key"},
		{"duplicate zone", map[string]string{"TNL_ZONE_A": "example.com", "TNL_ZONE_B": "example.com"}, nil, "TNL_ZONE_B: Zone example.com is also on TNL_ZONE_A"},
		{"both forms of a line kind", map[string]string{"TNL_ZONE_A": "example.com"}, []string{"--zone", "other.org"}, "--zone and TNL_ZONE_A are both set"},
		{"both forms of the key", map[string]string{"TNL_KEY_FILE": file, "TNL_KEY": keyText}, nil, "TNL_KEY_FILE and TNL_KEY are both set"},
		{"bad key", map[string]string{"TNL_KEY": "AAAA" + token}, nil, "TNL_KEY does not hold a WireGuard private key"},
		{"token in a flag line", nil, []string{"--zone", "example.com --wildcard cloudflare --token " + token}, "--token is accepted only in a TNL_ZONE_* variable"},
		{"routes on the edge", map[string]string{"TNL_ROUTE_PHOTOS": "photos.example.com http://x:1"}, nil, "TNL_ROUTE_PHOTOS: Routes are declared on the Outpost; set TNL_ROUTE_* there"},
		{"did you mean", map[string]string{"TNL_ZONES_MAIN": "example.com", "TNL_KEY_FIL": otherKey}, nil, "did you mean TNL_KEY_FILE"},
	}
	for _, c := range cases {
		for k, v := range c.env {
			t.Setenv(k, v)
		}
		_, err := parse(t, c.args...)
		var ce configError
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		} else if code := exitCode(err); code != 78 {
			_ = ce
			t.Errorf("%s: exit %d", c.name, code)
		}
		if err != nil {
			for _, secret := range []string{token, keyText, otherKey} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("%s: the error carries a secret", c.name)
				}
			}
		}
		for k := range c.env {
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
	}
}

func TestAZoneTokenInAVariableLineIsKeptAndNeverInTheUsage(t *testing.T) {
	clearEdgeEnv(t)
	t.Setenv("TNL_ZONE_MAIN", "example.com --wildcard cloudflare --token tok-S3CRET")
	s, err := parse(t)
	if err != nil || len(s.cfg.Zones) != 1 || s.cfg.Zones[0].Token != "tok-S3CRET" || s.cfg.Zones[0].Provider != "cloudflare" {
		t.Fatalf("%+v %v", s.cfg.Zones, err)
	}
}
