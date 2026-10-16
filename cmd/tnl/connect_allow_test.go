package main

import (
	"strings"
	"testing"
)

func TestConnectAllowFlagAndEnvironment(t *testing.T) {
	clearConnectEnv(t)
	t.Setenv("TNL_ALLOW", "")
	s, err := parseConnect(t)
	if err != nil || s.cfg.Allow != nil {
		t.Fatalf("%+v %v", s.cfg.Allow, err)
	}
	t.Setenv("TNL_ALLOW", "10.0.0.0/8")
	s, err = parseConnect(t)
	if err != nil || s.cfg.Allow.String() != "10.0.0.0/8" {
		t.Fatalf("%v %v", s.cfg.Allow, err)
	}
	s, err = parseConnect(t, "--allow", "192.168.1.0/24,localhost:3000")
	if err != nil || s.cfg.Allow.String() != "192.168.1.0/24,localhost:3000" {
		t.Fatalf("%v %v", s.cfg.Allow, err)
	}
}

func TestConnectMalformedAllowExits78(t *testing.T) {
	clearConnectEnv(t)
	t.Setenv("TNL_ALLOW", "")
	_, err := parseConnect(t, "--allow", "192.168.1.0/99")
	if exitCode(err) != 78 || !strings.Contains(err.Error(), "192.168.1.0/99") {
		t.Fatalf("err=%v code=%d", err, exitCode(err))
	}
	t.Setenv("TNL_ALLOW", "nonsense")
	if _, err := parseConnect(t); exitCode(err) != 78 {
		t.Fatalf("TNL_ALLOW: err=%v", err)
	}
}
