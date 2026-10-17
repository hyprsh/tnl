//go:build !outpost

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/credential"
	"github.com/hyprsh/tnl/internal/keys"
)

var envLine = regexp.MustCompile(`^\s+(TNL_[A-Z_]+): (.*)$`)

// composeEnv reads the TNL_* lines of a Compose example's environment block.
func composeEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, l := range strings.Split(string(b), "\n") {
		if m := envLine.FindStringSubmatch(l); m != nil {
			env[m[1]] = strings.Trim(m[2], `"`)
		}
	}
	return env
}

// The Compose examples are the spec's declared setup, and the lines in them
// parse as written once the placeholders are filled in.
func TestTheComposeExamplesParseAsWrittenAndMatchTheSpec(t *testing.T) {
	spec, err := os.ReadFile("../../docs/spec/v1.md")
	if err != nil {
		t.Fatal(err)
	}
	edgeEnv := composeEnv(t, "../../examples/compose/edge.yaml")
	outpostEnv := composeEnv(t, "../../examples/compose/outpost.yaml")
	for _, env := range []map[string]string{edgeEnv, outpostEnv} {
		for k, v := range env {
			if !strings.Contains(string(spec), k+": "+v) && !strings.Contains(string(spec), k+": \""+v+"\"") {
				t.Errorf("the spec's Compose example has no %s: %s", k, v)
			}
		}
	}

	// Fill in what the operator would: the key file, the Outpost's line and its credential.
	priv, _, _ := keys.Generate()
	keyText, _ := priv.MarshalText()
	keyFile := t.TempDir() + "/tnl-key"
	os.WriteFile(keyFile, append(keyText, '\n'), 0o600)
	cred, pub, _ := credential.Mint(priv, "nas", edgeEnv["TNL_ADDRESS"], 51820)
	pubText, _ := pub.MarshalText()
	credFile := t.TempDir() + "/tnl-credential"
	os.WriteFile(credFile, []byte(cred.Encode()), 0o600)

	clearEdgeEnv(t)
	for k, v := range edgeEnv {
		switch k {
		case "TNL_KEY_FILE":
			v = keyFile
		case "TNL_OUTPOST_NAS":
			v = strings.Replace(v, "6WvT…=", string(pubText), 1)
		}
		t.Setenv(k, v)
	}
	s, err := parse(t)
	if err != nil {
		t.Fatalf("the Edge example: %v", err)
	}
	if len(s.cfg.Zones) != 1 || len(s.cfg.Outposts) != 1 || s.cfg.Key == nil || s.cfg.Outposts[0].Key != pub {
		t.Fatalf("%+v", s.cfg)
	}

	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "TNL_") {
			t.Setenv(name, "")
			os.Unsetenv(name)
		}
	}
	for k, v := range outpostEnv {
		if k == "TNL_CREDENTIAL_FILE" {
			v = credFile
		}
		t.Setenv(k, v)
	}
	c, err := parseConnect(t)
	if err != nil {
		t.Fatalf("the Outpost example: %v", err)
	}
	if len(c.cfg.Declaration) != 2 || c.cfg.Credential != cred.Encode() {
		t.Fatalf("%+v", c.cfg)
	}
	if got, _ := credential.Decode(c.cfg.Credential); got.TunnelAddr != addr.Derive("nas") {
		t.Fatalf("%+v", got)
	}
}

// The README's quick start shows the Compose examples as they are, less the
// marker that tells release-please which line holds the version.
func TestTheReadmeShowsTheComposeExamplesAsTheyAre(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile("(?s)```yaml\n(.*?)```").FindAllStringSubmatch(string(readme), -1)
	files := []string{"../../examples/compose/edge.yaml", "../../examples/compose/outpost.yaml"}
	if len(blocks) != len(files) {
		t.Fatalf("the README has %d yaml blocks, want %d", len(blocks), len(files))
	}
	for i, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.ReplaceAll(string(b), " # x-release-please-version", "")
		if blocks[i][1] != want {
			t.Errorf("the README's yaml block %d differs from %s:\n%s", i+1, f, blocks[i][1])
		}
	}
}
