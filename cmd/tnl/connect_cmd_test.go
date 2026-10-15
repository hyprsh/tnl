package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parseConnect(t *testing.T, args ...string) (connectSettings, error) {
	t.Helper()
	return parseConnectFlags(args, io.Discard)
}

func clearConnectEnv(t *testing.T) {
	for _, v := range []string{"TNL_TOKEN", "TNL_STATE_DIR", "TNL_LOG_LEVEL", "TNL_LOG_FORMAT", "XDG_STATE_HOME"} {
		t.Setenv(v, "")
	}
}

func TestConnectDefaults(t *testing.T) {
	clearConnectEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	s, err := parseConnect(t)
	if err != nil {
		t.Fatal(err)
	}
	if s.cfg.Token != "" || s.cfg.StateDir != filepath.Join(home, ".local/state/tnl-outpost") || s.log.Level != "info" || s.log.Format != "text" {
		t.Fatalf("%+v %+v", s.cfg, s.log)
	}
}

func TestConnectStateDirFollowsXDGStateHome(t *testing.T) {
	clearConnectEnv(t)
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	s, err := parseConnect(t)
	if err != nil || s.cfg.StateDir != "/xdg/state/tnl-outpost" {
		t.Fatalf("%+v %v", s.cfg, err)
	}
}

func TestConnectEnvironmentAndFlagPrecedence(t *testing.T) {
	clearConnectEnv(t)
	t.Setenv("TNL_TOKEN", "tnl1.env")
	t.Setenv("TNL_STATE_DIR", "/env/dir")
	t.Setenv("TNL_LOG_LEVEL", "debug")
	t.Setenv("TNL_LOG_FORMAT", "json")
	s, err := parseConnect(t)
	if err != nil || s.cfg.Token != "tnl1.env" || s.cfg.StateDir != "/env/dir" || s.log.Level != "debug" || s.log.Format != "json" {
		t.Fatalf("%+v %+v %v", s.cfg, s.log, err)
	}
	s, err = parseConnect(t, "--token", "tnl1.flag", "--state-dir", "/flag/dir", "--log-level", "warn", "--log-format", "text")
	if err != nil || s.cfg.Token != "tnl1.flag" || s.cfg.StateDir != "/flag/dir" || s.log.Level != "warn" || s.log.Format != "text" {
		t.Fatalf("%+v %+v %v", s.cfg, s.log, err)
	}
}

func TestConnectBadFlagsAreConfigErrors(t *testing.T) {
	clearConnectEnv(t)
	for _, args := range [][]string{{"--nope"}, {"--log-level", "loud"}, {"extra"}} {
		_, err := parseConnect(t, args...)
		var ce configError
		if !errors.As(err, &ce) {
			t.Errorf("%v: err=%v", args, err)
		}
	}
	if _, err := parseConnect(t, "-h"); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h: %v", err)
	}
}

func TestConnectWithAnUnknownMarkerExits78AndLeavesNoState(t *testing.T) {
	clearConnectEnv(t)
	dir := filepath.Join(t.TempDir(), "state")
	err := commands["connect"]([]string{"--token", "tnl2.x", "--state-dir", dir})
	if exitCode(err) != 78 || !strings.Contains(err.Error(), "newer tnl") {
		t.Fatalf("err=%v code=%d", err, exitCode(err))
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("state touched: %v", err)
	}
}
