package main

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// socketDirs are the state directories the CLI searches, in order, when none
// is pinned. Tests replace it so they never touch the real /var/lib.
var socketDirs = func() []string {
	return []string{outpostStateDir(), "/var/lib/tnl-outpost", "/var/lib/tnl"}
}

// discoverStateDir picks the state directory whose tnl.sock the CLI talks to
// (spec: State directories and sockets). A pinned directory is used as it is.
func discoverStateDir(pinned string) (string, error) {
	if pinned != "" {
		return pinned, nil
	}
	var live, denied, checked []string
	for _, dir := range socketDirs() {
		sock := filepath.Join(dir, "tnl.sock")
		checked = append(checked, sock)
		switch probe(sock) {
		case socketLive:
			live = append(live, dir)
		case socketDenied:
			denied = append(denied, sock)
		}
	}
	switch {
	case len(live) == 1:
		return live[0], nil
	case len(live) > 1:
		socks := make([]string, len(live))
		for i, dir := range live {
			socks[i] = filepath.Join(dir, "tnl.sock")
		}
		return "", fmt.Errorf("more than one tnl process is running here (%s); pass --state-dir or set TNL_STATE_DIR to pick one", strings.Join(socks, ", "))
	case len(denied) > 0:
		return "", fmt.Errorf("can't open %s: permission denied; run with sudo", strings.Join(denied, ", "))
	}
	return "", fmt.Errorf("no Edge or Outpost is running: nothing answers on %s (start one, or pass --state-dir or set TNL_STATE_DIR to the state directory of the one to talk to)", strings.Join(checked, ", "))
}

type socketState int

const (
	socketAbsent socketState = iota // nothing there, or nothing listening
	socketLive
	socketDenied // there, but this user can't open it
)

func probe(sock string) socketState {
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	switch {
	case err == nil:
		conn.Close()
		return socketLive
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return socketDenied
	}
	return socketAbsent
}
