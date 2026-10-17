//go:build !outpost

package main

import (
	"strings"
	"testing"
)

func TestMissingArgumentPointsAtHelp(t *testing.T) {
	runningEdge(t)
	for _, args := range [][]string{{"route", "rm"}, {"route", "add"}, {"route", "edit"}, {"route"}, {"status", "x"}, {"outpost", "add"}, {"backup"}} {
		_, code, err := tnl(args...)
		if err == nil || code != 1 || !strings.Contains(err.Error(), "tnl help "+args[0]) {
			t.Errorf("%v: code %d, err %v", args, code, err)
		}
	}
}

func TestBadDaemonFlagIsAConfigErrorWithAPointer(t *testing.T) {
	_, code, err := tnl("edge", "--nonesuch")
	if code != 78 || err == nil || !strings.Contains(err.Error(), "tnl help edge") {
		t.Errorf("code %d, err %v", code, err)
	}
}
