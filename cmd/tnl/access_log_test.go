//go:build !outpost

package main

import (
	"errors"
	"testing"
)

func TestEdgeAccessLogFlagAndEnvironment(t *testing.T) {
	t.Setenv("TNL_ADDRESS", "tnl.example.com")
	t.Setenv("TNL_ACCESS_LOG", "")
	s, err := parse(t)
	if err != nil || s.cfg.Web.AccessLog {
		t.Fatalf("default: %v, access log %v", err, s.cfg.Web.AccessLog)
	}
	s, err = parse(t, "--access-log")
	if err != nil || !s.cfg.Web.AccessLog {
		t.Fatalf("flag: %v, access log %v", err, s.cfg.Web.AccessLog)
	}
	t.Setenv("TNL_ACCESS_LOG", "true")
	s, err = parse(t)
	if err != nil || !s.cfg.Web.AccessLog {
		t.Fatalf("environment: %v, access log %v", err, s.cfg.Web.AccessLog)
	}
	s, err = parse(t, "--access-log=false")
	if err != nil || s.cfg.Web.AccessLog {
		t.Fatalf("flag over environment: %v, access log %v", err, s.cfg.Web.AccessLog)
	}
	t.Setenv("TNL_ACCESS_LOG", "maybe")
	_, err = parse(t)
	var ce configError
	if !errors.As(err, &ce) {
		t.Fatalf("a bad TNL_ACCESS_LOG is %v, want a config error", err)
	}
}
