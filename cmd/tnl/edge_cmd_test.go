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

func TestConfigErrorsExit78(t *testing.T) {
	if exitCode(configError{errors.New("x")}) != 78 || exitCode(errors.New("x")) != 1 || exitCode(nil) != 0 {
		t.Fatal("wrong exit codes")
	}
}
