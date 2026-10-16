package main

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/credential"
	"github.com/hyprsh/tnl/internal/keys"
)

func sampleCredential(t *testing.T) string {
	t.Helper()
	priv, _, _ := keys.Generate()
	_, edge, _ := keys.Generate()
	return credential.Credential{Name: "nas", Address: "edge.example.com", Port: 51820, EdgeKey: edge, PrivateKey: priv,
		TunnelAddr: netip.MustParseAddr("100.85.233.86")}.Encode()
}

func clearCredentialEnv(t *testing.T) {
	t.Helper()
	clearConnectEnv(t)
	for _, v := range []string{"TNL_CREDENTIAL", "TNL_CREDENTIAL_FILE"} {
		t.Setenv(v, "")
	}
}

func TestConnectTakesTheCredentialFromAFileOrAVariable(t *testing.T) {
	clearCredentialEnv(t)
	cred := sampleCredential(t)
	file := filepath.Join(t.TempDir(), "cred")
	os.WriteFile(file, []byte(cred+"\n"), 0o600)

	t.Setenv("TNL_CREDENTIAL_FILE", file)
	s, err := parseConnect(t)
	if err != nil || s.cfg.Credential != cred {
		t.Fatalf("file variable: %q %v", s.cfg.Credential, err)
	}
	t.Setenv("TNL_CREDENTIAL_FILE", "")
	s, err = parseConnect(t, "--credential-file", file)
	if err != nil || s.cfg.Credential != cred {
		t.Fatalf("flag: %q %v", s.cfg.Credential, err)
	}
	t.Setenv("TNL_CREDENTIAL", cred)
	s, err = parseConnect(t)
	if err != nil || s.cfg.Credential != cred {
		t.Fatalf("variable: %q %v", s.cfg.Credential, err)
	}
}

func TestConnectRefusesCredentialMistakesWithExit78AndNeverEchoesIt(t *testing.T) {
	clearCredentialEnv(t)
	cred := sampleCredential(t)
	file := filepath.Join(t.TempDir(), "cred")
	os.WriteFile(file, []byte(cred), 0o600)
	cases := []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{"both forms", map[string]string{"TNL_CREDENTIAL": cred, "TNL_CREDENTIAL_FILE": file}, nil, "TNL_CREDENTIAL_FILE and TNL_CREDENTIAL are both set"},
		{"a token with it", map[string]string{"TNL_CREDENTIAL": cred, "TNL_TOKEN": "tnl1.x"}, nil, "--token (TNL_TOKEN) and an Outpost credential are both set"},
		{"a join token as the credential", map[string]string{"TNL_CREDENTIAL": "tnl1.abcdef"}, nil, "join token, not an Outpost credential"},
		{"a credential as the token", map[string]string{"TNL_TOKEN": cred}, nil, ""},
		{"a damaged credential", map[string]string{"TNL_CREDENTIAL": "tnlc1.!!"}, nil, "damaged"},
		{"a missing file", nil, []string{"--credential-file", "/no/such/file"}, "--credential-file: /no/such/file is missing"},
	}
	for _, c := range cases {
		for k, v := range c.env {
			t.Setenv(k, v)
		}
		_, err := parseConnect(t, c.args...)
		if c.want == "" {
			// A credential in TNL_TOKEN parses; outpost.Run turns it away with a
			// friendly message (tested in the token package).
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		} else if exitCode(err) != 78 {
			t.Errorf("%s: exit %d", c.name, exitCode(err))
		}
		if err != nil && strings.Contains(err.Error(), strings.TrimPrefix(cred, "tnlc1.")) {
			t.Errorf("%s: the error carries the credential", c.name)
		}
		for k := range c.env {
			t.Setenv(k, "")
		}
	}
}
