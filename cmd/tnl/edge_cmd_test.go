//go:build !outpost

package main

import (
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
)

func parse(t *testing.T, args ...string) (edgeSettings, error) {
	t.Helper()
	return parseEdgeFlags(args, io.Discard)
}

func TestEdgeAddressIsRequired(t *testing.T) {
	t.Setenv("TNL_ADDRESS", "")
	_, err := parse(t)
	var ce configError
	if !errors.As(err, &ce) || !strings.Contains(err.Error(), "--address") {
		t.Fatalf("err=%v", err)
	}
}

func TestEdgeDefaults(t *testing.T) {
	for _, v := range []string{"TNL_STATE_DIR", "TNL_LOG_LEVEL", "TNL_LOG_FORMAT"} {
		t.Setenv(v, "")
	}
	s, err := parse(t, "--address", "tnl.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if s.cfg.Address != "tnl.example.com" || s.cfg.StateDir != "/var/lib/tnl" || s.log.Level != "info" || s.log.Format != "text" {
		t.Fatalf("%+v %+v", s.cfg, s.log)
	}
}

func TestEdgeEnvironmentAndFlagPrecedence(t *testing.T) {
	t.Setenv("TNL_ADDRESS", "env.example.com")
	t.Setenv("TNL_STATE_DIR", "/env/dir")
	t.Setenv("TNL_LOG_LEVEL", "debug")
	t.Setenv("TNL_LOG_FORMAT", "json")
	s, err := parse(t)
	if err != nil {
		t.Fatal(err)
	}
	if s.cfg.Address != "env.example.com" || s.cfg.StateDir != "/env/dir" || s.log.Level != "debug" || s.log.Format != "json" {
		t.Fatalf("%+v %+v", s.cfg, s.log)
	}
	s, err = parse(t, "--address", "flag.example.com", "--state-dir", "/flag/dir", "--log-level", "warn")
	if err != nil {
		t.Fatal(err)
	}
	if s.cfg.Address != "flag.example.com" || s.cfg.StateDir != "/flag/dir" || s.log.Level != "warn" {
		t.Fatalf("%+v %+v", s.cfg, s.log)
	}
}

func TestEdgeBadFlagsAreConfigErrors(t *testing.T) {
	t.Setenv("TNL_ADDRESS", "")
	for _, args := range [][]string{{"--nope"}, {"--address", "a", "--log-level", "loud"}, {"--address", "a", "extra"}} {
		_, err := parse(t, args...)
		var ce configError
		if !errors.As(err, &ce) {
			t.Errorf("%v: err=%v", args, err)
		}
	}
	if _, err := parse(t, "-h"); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h: %v", err)
	}
}

func TestEdgeWireGuardPort(t *testing.T) {
	t.Setenv("TNL_WIREGUARD_PORT", "")
	s, err := parse(t, "--address", "a")
	if err != nil || s.cfg.WireGuardPort != 51820 {
		t.Fatalf("default: %+v %v", s.cfg, err)
	}
	t.Setenv("TNL_WIREGUARD_PORT", "443")
	if s, err = parse(t, "--address", "a"); err != nil || s.cfg.WireGuardPort != 443 {
		t.Fatalf("env: %+v %v", s.cfg, err)
	}
	if s, err = parse(t, "--address", "a", "--wireguard-port", "4000"); err != nil || s.cfg.WireGuardPort != 4000 {
		t.Fatalf("flag: %+v %v", s.cfg, err)
	}
	for _, bad := range []string{"0", "70000", "x"} {
		var ce configError
		if _, err := parse(t, "--address", "a", "--wireguard-port", bad); !errors.As(err, &ce) {
			t.Errorf("%s: err=%v", bad, err)
		}
	}
}

func TestEdgeACMEFlagsAndEnvironment(t *testing.T) {
	t.Setenv("TNL_ACME_EMAIL", "")
	t.Setenv("TNL_ACME_CA", "")
	s, err := parse(t, "--address", "a")
	if err != nil || s.cfg.Web == nil || s.cfg.Web.ACMEEmail != "" || s.cfg.Web.ACMECA != "" {
		t.Fatalf("default (Let's Encrypt production is the empty CA): %+v %v", s.cfg.Web, err)
	}
	t.Setenv("TNL_ACME_EMAIL", "env@example.com")
	t.Setenv("TNL_ACME_CA", "https://env.example/dir")
	s, err = parse(t, "--address", "a")
	if err != nil || s.cfg.Web.ACMEEmail != "env@example.com" || s.cfg.Web.ACMECA != "https://env.example/dir" {
		t.Fatalf("environment: %+v %v", s.cfg.Web, err)
	}
	s, err = parse(t, "--address", "a", "--acme-email", "f@example.com", "--acme-ca", "https://flag.example/dir")
	if err != nil || s.cfg.Web.ACMEEmail != "f@example.com" || s.cfg.Web.ACMECA != "https://flag.example/dir" {
		t.Fatalf("flags: %+v %v", s.cfg.Web, err)
	}
}

func TestConfigErrorsExit78(t *testing.T) {
	if exitCode(configError{errors.New("x")}) != 78 || exitCode(errors.New("x")) != 1 || exitCode(nil) != 0 {
		t.Fatal("wrong exit codes")
	}
}
